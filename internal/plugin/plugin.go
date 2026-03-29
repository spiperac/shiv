package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

const callTimeout = 500 * time.Millisecond

// Plugin is a single loaded Lua script with its own isolated VM.
// mu serialises all VM access — gopher-lua LState is not goroutine-safe.
type Plugin struct {
	name string
	path string
	L    *lua.LState
	mu   sync.Mutex
}

// load reads the Lua file, registers the full API, executes the script,
// then calls on_load() if defined.
func load(path string, st *store.Store) (*Plugin, error) {
	name := filepath.Base(path)

	L := lua.NewState(lua.Options{
		SkipOpenLibs: false,
	})

	p := &Plugin{name: name, path: path, L: L}

	// Register API before executing the script so on_load can use it.
	registerAPI(L, st)

	if err := L.DoFile(path); err != nil {
		L.Close()
		return nil, fmt.Errorf("plugin %s: load: %w", name, err)
	}

	// Call on_load() if defined — errors are logged but not fatal.
	if L.GetGlobal("on_load") != lua.LNil {
		if err := p.callNoReturn("on_load", lua.LNil); err != nil {
			logger.Error("plugin %s: on_load: %v", name, err)
		}
	}

	return p, nil
}

// callNoReturn invokes a Lua function that returns nothing.
// Holds mu for the duration. Enforces callTimeout. Recovers panics.
func (p *Plugin) callNoReturn(fn string, arg lua.LValue) (retErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic: %v", r)
		}
	}()

	f := p.L.GetGlobal(fn)
	if f == lua.LNil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	p.L.SetContext(ctx)
	defer p.L.SetContext(context.Background())

	params := lua.P{Fn: f, NRet: 0, Protect: true}
	if arg != lua.LNil {
		return p.L.CallByParam(params, arg)
	}
	return p.L.CallByParam(params)
}

// callWithBuilder builds a Lua table and calls fn with it in a single locked
// section, returning the result table. builder receives the VM and must
// construct and return the argument table — it runs under mu. The Lua call
// also runs under mu, so no VM access escapes the lock at any point.
// Returns the returned table, or nil if the function returned nothing.
func (p *Plugin) callWithBuilder(fn string, builder func(L *lua.LState) *lua.LTable) (ret *lua.LTable, retErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic: %v", r)
		}
	}()

	f := p.L.GetGlobal(fn)
	if f == lua.LNil {
		return nil, nil
	}

	arg := builder(p.L)

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	p.L.SetContext(ctx)
	defer p.L.SetContext(context.Background())

	if err := p.L.CallByParam(lua.P{Fn: f, NRet: 1, Protect: true}, arg); err != nil {
		return nil, err
	}

	top := p.L.Get(-1)
	p.L.Pop(1)

	if tbl, ok := top.(*lua.LTable); ok {
		return tbl, nil
	}
	return nil, nil
}

// close shuts down the Lua VM.
func (p *Plugin) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.L.Close()
}

// has returns true if the plugin defines the named global function.
func (p *Plugin) has(fn string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.L.GetGlobal(fn) != lua.LNil
}

// loadDir scans dir for *.lua files and loads each as a Plugin.
// Files that fail to load are logged and skipped.
func loadDir(dir string, st *store.Store) ([]*Plugin, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("plugin: read dir %s: %w", dir, err)
	}

	var plugins []*Plugin
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lua" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		p, err := load(path, st)
		if err != nil {
			logger.Error("plugin: skip %s: %v", e.Name(), err)
			continue
		}
		logger.Info("plugin: loaded %s", e.Name())
		plugins = append(plugins, p)
	}
	return plugins, nil
}
