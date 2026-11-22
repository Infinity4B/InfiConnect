package app

import (
	"github.com/egoist/mygo"
	"github.com/inficonnect/inficonnect/app/sysproxy"
	"github.com/inficonnect/inficonnect/core"
)

// Typed events pushed from Go to the frontend (picked up by `mygo generate`).
var (
	EventStateChanged       = mygo.NewEvent[core.StatusSnapshot]("vpn:state")
	EventSessionsChanged    = mygo.NewEvent[[]core.StatusSnapshot]("vpn:sessions")
	EventChallengeRequested = mygo.NewEvent[core.Challenge]("vpn:challenge")
	EventChallengeResolved  = mygo.NewEvent[string]("vpn:challenge-resolved")
	EventLogReceived        = mygo.NewEvent[core.LogEntry]("vpn:log")
	EventTrafficUpdated     = mygo.NewEvent[core.TrafficStats]("vpn:traffic")
	EventResourcesUpdated   = mygo.NewEvent[core.ResourceSnapshot]("vpn:resources")
	EventSystemProxyChanged = mygo.NewEvent[sysproxy.State]("vpn:sysproxy")
	EventProfilesChanged    = mygo.NewEvent[ProfilesListResult]("vpn:profiles")
)
