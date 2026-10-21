//go:build qualification

package mcp

import (
	"context"
	"os/exec"
	"sync"
)

// ObserveRegistrationCommandsForQualification exposes commands only in a tagged
// qualification binary. Callers must synchronize terminal owner close before
// inspecting ProcessState; this observer never waits, signals or changes lifetime.
func ObserveRegistrationCommandsForQualification() (snapshot func() []*exec.Cmd, restore func()) {
	request, stateful := registrationCommandContext, registrationCommand
	var mu sync.Mutex
	var commands []*exec.Cmd
	observe := func(cmd *exec.Cmd) *exec.Cmd {
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, cmd)
		return cmd
	}
	registrationCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return observe(request(ctx, name, args...))
	}
	registrationCommand = func(name string, args ...string) *exec.Cmd {
		return observe(stateful(name, args...))
	}
	return func() []*exec.Cmd {
			mu.Lock()
			defer mu.Unlock()
			return append([]*exec.Cmd(nil), commands...)
		}, func() {
			registrationCommandContext, registrationCommand = request, stateful
		}
}
