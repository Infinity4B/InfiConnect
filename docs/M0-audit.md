# M0 上游源码核实与架构选型结论

> 核实基线：`github.com/mythologyli/zju-connect` commit `8576e7a16076c4175d6dbe276573a353cf7260be` (`v1.3.2-0.20261101145054-8576e7a16076`)、`github.com/egoist/mygo` `v0.9.2`

---

## 1. `authchallenge.Handler` 注入点核实（原 3.4 待确认）

**结论：上游已经原生支持注入自定义 `authchallenge.Handler`，无需为此 fork 上游。**

- **EasyConnect**：`client/easyconnect/client.go:48` 的 `easyconnect.Options` 已包含 `ChallengeHandler authchallenge.Handler` 字段（未传时默认回退到 `authchallenge.NewCLIHandler`）。
- **aTrust**：`client/atrust/client.go:49` 的 `atrust.SetupOptions` 已包含 `ChallengeHandler authchallenge.Handler` 字段，并在 `SetupContext` 中透传至 `client/atrust/auth/auth.go:187` 的 `auth.LoginOptions.ChallengeHandler`。
- **可取消性**：`atrust.Client.SetupContext(ctx, opts)` 原生支持 `context.Context` 取消；`easyconnect.Client.Setup()` 绑定在 `c.lifecycleCtx` 上，调用 `c.Close()` 可中断正在进行的建连。
- **四类挑战覆盖**：`HandleCodeChallenge`（sms/totp/radius/device）、`HandleTextCaptcha`、`HandleClickCaptcha`（支持回传原图坐标与宽高）、`HandleExternalLogin`（CAS 回调路径 `/passport/v1/auth/cas?ticket=...`，OAuth2 回调路径 `/passport/v1/auth/httpsOauth2?code=...`）均已完整走 `ChallengeHandler`。

---

## 2. `mygo` 导航拦截与桌面能力核实（原 4 待确认）

**结论：`mygo v0.9.2` 已原生支持子窗口导航拦截与全套桌面能力。**

- **外部登录导航拦截**：`mygo.NewWindow(WindowOptions{Page: PageOptions{Untrusted: true}})` + `win.Page().OnWillNavigate(func(e *mygo.NavigateEvent))` 可拦截即将跳转的回调地址并调用 `e.PreventDefault()`，随后关闭子窗口并将完整回调 URL 交付 `Engine.SubmitChallenge`。同时保留「在系统浏览器打开 + 手动粘贴回调 URL」兜底方案。
- **系统凭据存储**：`mygo.Secrets`（纯 Go，无需 cgo）直接支持 macOS Keychain、Linux Secret Service (libsecret)、Windows Credential Manager。
- **托盘 / 开机启动 / 自动更新**：`mygo.NewTray`、`mygo.App.SetOpenAtLogin`、`mygo.Updater` 均已内置。

---

## 3. 生命周期与方案 A / B 选型结论（原 3.5 最大风险点）

### 3.1 上游生命周期审计

阅读上游源码发现，若直接在 GUI 主进程内运行未修改的 `zju-connect` 协议栈，存在以下**进程级退出 / 泄漏风险**：

1. **`log.Fatalf` 直接 `os.Exit(1)`**：
   - `client/atrust/session.go:42`（会话刷新失败）
   - `client/atrust/ip.go:23`、`l3tunnelconn.go:530,939`、`tcptunnel.go:171`（aTrust 会话失效）
2. **`os.Exit(2)` 与未捕获 `panic`**：
   - `stack/gvisor/stack.go:118` 在收到服务端 `ErrSangforShutdown` 时直接调用 `os.Exit(2)`
   - `stack/gvisor/stack.go:124,234,244` 在 `l3Conn` 读写出错且 `!hook_func.IsTerminal()` 时调用 `panic(err)`，而 `internal/hook_func` 无法从外部调用
   - `stack/gvisor/stack.go` 没有 `Close()` 方法，不会调用 `gvisorStack.Close()` 与 `l3Conn.Close()`，多次断开重连会泄漏 gVisor goroutine
3. **全局状态与不可取消服务**：
   - `service/socks.go`、`service/http.go`、`service/dns.go` 均向 `internal/hook_func` 注册清理函数且不返回可关闭句柄；`ServeSocks5` 监听失败会直接 `panic`
   - `client/easyconnect/client.go:275` 每次 `Setup()` 都会向全局 `internal/hook_func` 追加条目

### 3.2 探针程序实测（`cmd/probe`）

- 我们在 `core/` 中实现了自管、可取消、带流量统计与可热切换拨号器的 `ProxyServer`（替代不可取消的 `service.ServeSocks5` / `service.ServeHTTP`），并运行 `go run ./cmd/probe --mock --cycles 3` 与 `go test -race ./core/...`。
- 结果：3 次「连接 → 经代理访问 → 断开」循环全部通过，本地 SOCKS5 / HTTP 端口 100% 立即释放，goroutine 净增量 `+0`。

### 3.3 最终选型：**采用方案 B（自启 `--core` 子进程 + stdio JSON-RPC），同时保留 `InProcessEngine` 供 `--core` 与测试复用**

理由：
1. **彻底隔离上游 `os.Exit` / `log.Fatalf` / `panic`**：上游在 aTrust 会话过期或服务端 SHUTDOWN 时会直接退出进程；采用方案 B 后，即使上游触发致命退出，也仅结束 `--core` 子进程，GUI 主进程保持存活并由监督器自动按指数退避重启重连。
2. **零残留拆除**：每次断开先通过 JSON-RPC 优雅关闭代理监听与会话，再终止子进程，操作系统内核 100% 回收所有 gVisor goroutine、socket 与 `internal/hook_func` 全局状态。
3. **免长期维护上游 fork**：主项目直接引用上游官方 commit（零 `replace` 到私有 fork），降低上游同步负担；后续向上游提交可嵌入性 PR（移除库内 `log.Fatalf`/`os.Exit`、为 `gvisor.Stack` 增加 `Close()`）合入后，也可随时无缝切换回方案 A。
4. **平滑衔接 v2 TUN 模式**：v2 引入 TUN 时可直接沿用 `--core` 子进程架构对其进行单独提权，无需改动 `app/` 与 `frontend/`。
