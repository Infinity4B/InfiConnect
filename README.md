<p align="center">
  <img src="assets/icon.svg" width="76" height="76" alt="InfiConnect Logo">
</p>

<h1 align="center">InfiConnect</h1>

<p align="center">
  Open-source, cross-platform desktop GUI client for <strong>EasyConnect</strong> &amp; <strong>aTrust</strong> VPNs, powered by <a href="https://github.com/mythologyli/zju-connect">ZJU-Connect</a> and <a href="https://github.com/egoist/mygo">MyGo</a>.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg" alt="AGPL-3.0"></a>
  <img src="https://img.shields.io/badge/Platforms-macOS%20%7C%20Windows%20%7C%20Linux-informational" alt="Platforms">
</p>

---

## Features / 核心特性

- **Dual Protocol Support**：完整支持 **EasyConnect** 与 **aTrust**，直接以 Go 库方式嵌入 `github.com/mythologyli/zju-connect`，无需附带或调用外部二进制。
- **User-Space Netstack (No Root Required)**：基于 gVisor 用户态网络栈提供本地 **SOCKS5** 与 **HTTP/HTTPS** 代理，无需管理员权限或安装内核驱动。
- **All Interactive Auth Challenges in GUI**：
  - 短信 / TOTP / Radius / 设备码二次认证
  - 图形文字验证码
  - 点选验证码（点击取点并自动按显示尺寸换算回原图坐标）
  - CAS / OAuth2 外部登录（内置隔离 WebView 自动拦截回调 URL，同时保留系统浏览器 + 手动粘贴回调地址兜底）
- **Multi-Profile & ZJU-Connect TOML Migration**：多档案管理、支持一键导入已有的 ZJU-Connect `config.toml`，敏感凭据（密码、TOTP 密钥、证书密码、SOCKS 密码）均通过 AES-256-GCM 加密保存在应用私有配置目录的本地凭据库（`0600` 权限，与 `profiles.json` 分离，不再因 ad-hoc 签名反复弹出 macOS 钥匙串密码框）。
- **System Proxy with Crash Recovery**：一键开关系统代理（macOS `networksetup` / Windows 注册表 + WinINet 刷新 / Linux GNOME `gsettings`），支持正常退出、信号中断与异常崩溃后启动自动还原。
- **System Tray & Auto-Reconnect**：状态指示托盘、快速切换档案与复制代理地址、断线指数退避自动重连（aTrust 自动复用持久化会话避免重复弹验证码）。
- **Resources, Route Checker & Sanitized Logs**：实时查看服务端下发的 IP 网段、域名规则与 DNS 覆盖，输入任意地址即可判断是否走 VPN；支持分级日志过滤与**一键导出脱敏诊断日志**。

---

## Architecture / 分层架构

自下而上四层，依赖严格向下：

```text
frontend/ (React + TypeScript + Vite)
   │  Typed bindings & events (frontend/src/mygo.ts)
   ▼
app/ (MyGo bindings, Tray, Encrypted Secret Vault, System Proxy, External Login WebView)
   │  Depends ONLY on core.Engine interface
   ▼
core/ (Engine interface, State Machine, Challenge Bridge, TOML Importer, Route Checker, Log Redactor)
   ├── core/rpc/  ── Scheme-B `--core` subprocess worker + stdio NDJSON JSON-RPC
   └── core/inprocess.go ── Sole package allowed to import github.com/mythologyli/zju-connect
```

- **为什么采用 `--core` 子进程模式（方案 B）？**
  详见 [M0 架构审计报告](docs/M0-audit.md)。上游库部分错误路径会直接调用 `log.Fatalf` / `os.Exit` 或残留全局状态；通过自启 `--core` 子进程隔离协议栈，既保证了断开时 100% 释放端口与 goroutine，又确保协议层异常退出不会拖垮 GUI 主进程（监督器会自动按指数退避重连），同时也为 v2 提权 TUN 模式预留了清晰边界。

---

## Development & Build / 开发与构建

### Prerequisites

- Go `1.25+` (module uses `go 1.27.3` toolchain)
- Node.js `20+` & npm
- Platform WebView toolchain (`go tool mygo doctor` verifies your environment)

### Run Tests & Headless Probe

```bash
# Run Go unit & race tests (core, rpc, app, sysproxy)
go test -race ./...

# Verify M0 probe (3x Connect -> Proxy GET -> Disconnect, zero port/goroutine leak)
go run ./cmd/probe --mock --cycles 3

# Build TypeScript frontend
npm --prefix frontend ci
npm --prefix frontend run build
```

### Run Desktop App in Dev / Build Installers

```bash
# Regenerate MyGo TS bindings after modifying app/ exported methods or events
go tool mygo generate

# Launch desktop app in dev mode with Vite HMR
go tool mygo dev

# Build production bundle (.app/.dmg on macOS, installer on Windows, AppImage/deb on Linux)
go tool mygo build
```

---

## Acknowledgments / 致谢

- [ZJU-Connect](https://github.com/mythologyli/zju-connect) — 本项目直接引用的核心 VPN 协议库（AGPL-3.0）。
- [EasierConnect](https://github.com/Hagb/docker-easyconnect) — ZJU-Connect 所基于的开源协议逆向与实现项目。
- [MyGo](https://github.com/egoist/mygo) — 轻量级 Go + 系统 WebView 跨平台桌面框架。

---

## License & Disclaimer / 开源许可与免责声明

- **License**: 本项目整体采用 **GNU Affero General Public License v3.0 (AGPL-3.0)** 开源，详见 [LICENSE](LICENSE)。
- **Disclaimer**: InfiConnect 为社区独立开发的开源客户端，**非深信服（Sangfor Technologies）官方产品**，与深信服无任何隶属或合作关系。请使用者严格遵守所在学校、企业或机构的网络安全与 VPN 使用规定；因违规使用产生的任何后果由使用者自行承担。
