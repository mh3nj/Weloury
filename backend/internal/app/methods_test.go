package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"weloury/internal/containers"
	"weloury/internal/eventbus"
	"weloury/internal/ipc"
	"weloury/internal/system"
	"weloury/pkg/fwp"
)

// fakeProvider implements system.Provider without touching a real OS, so
// these tests exercise registerMethods's wiring and error-translation
// logic (toFWPError) independent of platform - the platform-specific
// providers themselves are tested in internal/system.
type fakeProvider struct {
	systemInfo     system.SystemInfo
	cpuInfo        system.CPUInfo
	cpuUsage       system.CPUUsage
	memoryInfo     system.MemoryInfo
	disks          []system.DiskInfo
	interfaces     []system.NetworkInterface
	netIOCounters  []system.NetIOCounters
	netConnections []system.NetworkConnection
	processes      []system.ProcessInfo
	processDetail  system.ProcessDetail
	services       []system.ServiceInfo
	serviceDetail  system.ServiceDetail
	returnedErr    error

	terminateCalls    []terminateCall
	serviceActionCall *serviceActionCall
	gpus              []system.GPUInfo
	gpuUsage          system.GPUUsage

	startupEntries     []system.StartupEntry
	startupToggleCalls []struct {
		id      string
		enabled bool
	}
	startupRemovedIDs []string
}

type terminateCall struct {
	pid   int32
	force bool
}

type serviceActionCall struct {
	action  string
	name    string
	enabled bool // only meaningful when action == "set_enabled"
}

func (f *fakeProvider) SystemInfo(context.Context) (system.SystemInfo, error) {
	return f.systemInfo, f.returnedErr
}
func (f *fakeProvider) CPUInfo(context.Context) (system.CPUInfo, error) {
	return f.cpuInfo, f.returnedErr
}
func (f *fakeProvider) CPUUsage(context.Context, time.Duration) (system.CPUUsage, error) {
	return f.cpuUsage, f.returnedErr
}
func (f *fakeProvider) MemoryInfo(context.Context) (system.MemoryInfo, error) {
	return f.memoryInfo, f.returnedErr
}
func (f *fakeProvider) Disks(context.Context) ([]system.DiskInfo, error) {
	return f.disks, f.returnedErr
}
func (f *fakeProvider) NetworkInterfaces(context.Context) ([]system.NetworkInterface, error) {
	return f.interfaces, f.returnedErr
}
func (f *fakeProvider) NetworkIOCounters(context.Context) ([]system.NetIOCounters, error) {
	return f.netIOCounters, f.returnedErr
}
func (f *fakeProvider) NetworkConnections(context.Context) ([]system.NetworkConnection, error) {
	return f.netConnections, f.returnedErr
}
func (f *fakeProvider) ListProcesses(context.Context) ([]system.ProcessInfo, error) {
	return f.processes, f.returnedErr
}
func (f *fakeProvider) ProcessDetail(context.Context, int32, time.Duration) (system.ProcessDetail, error) {
	return f.processDetail, f.returnedErr
}
func (f *fakeProvider) TerminateProcess(_ context.Context, pid int32, force bool) error {
	f.terminateCalls = append(f.terminateCalls, terminateCall{pid: pid, force: force})
	return f.returnedErr
}
func (f *fakeProvider) ListServices(context.Context) ([]system.ServiceInfo, error) {
	return f.services, f.returnedErr
}
func (f *fakeProvider) ServiceDetail(context.Context, string) (system.ServiceDetail, error) {
	return f.serviceDetail, f.returnedErr
}
func (f *fakeProvider) StartService(_ context.Context, name string) error {
	f.serviceActionCall = &serviceActionCall{action: "start", name: name}
	return f.returnedErr
}
func (f *fakeProvider) StopService(_ context.Context, name string) error {
	f.serviceActionCall = &serviceActionCall{action: "stop", name: name}
	return f.returnedErr
}
func (f *fakeProvider) RestartService(_ context.Context, name string) error {
	f.serviceActionCall = &serviceActionCall{action: "restart", name: name}
	return f.returnedErr
}
func (f *fakeProvider) SetServiceEnabled(_ context.Context, name string, enabled bool) error {
	f.serviceActionCall = &serviceActionCall{action: "set_enabled", name: name, enabled: enabled}
	return f.returnedErr
}

// Disk-health and startup surfaces: stubs returning ErrUnavailable by
// default - the fake exists to exercise IPC wiring, not health data
// (see TestRegisterMethodsDiskHealthUnavailable) - with call recording
// on the startup mutators so parameters round-tripping through the FWP
// layer can be asserted.
func (f *fakeProvider) DiskHealth(context.Context) ([]system.DiskHealth, error) {
	return nil, system.ErrUnavailable
}
func (f *fakeProvider) ListStartupEntries(context.Context) ([]system.StartupEntry, error) {
	return f.startupEntries, nil
}
func (f *fakeProvider) SetStartupEntryEnabled(_ context.Context, id string, enabled bool) error {
	f.startupToggleCalls = append(f.startupToggleCalls, struct {
		id      string
		enabled bool
	}{id, enabled})
	return nil
}
func (f *fakeProvider) AddStartupEntry(context.Context, string, string, []string) error {
	return nil
}
func (f *fakeProvider) RemoveStartupEntry(_ context.Context, id string) error {
	f.startupRemovedIDs = append(f.startupRemovedIDs, id)
	return nil
}

// GPU surface: fakes carry a small inventory and usage so gpu.* wiring
// can be asserted without a real adapter (see TestRegisterMethodsGPURoundTrips).
func (f *fakeProvider) ListGPUs(context.Context) ([]system.GPUInfo, error) {
	if f.returnedErr != nil {
		return nil, f.returnedErr
	}
	return f.gpus, nil
}

func (f *fakeProvider) GPUUsage(_ context.Context, index int) (system.GPUUsage, error) {
	if f.returnedErr != nil {
		return system.GPUUsage{}, f.returnedErr
	}
	if index < 0 || index >= len(f.gpus) {
		return system.GPUUsage{}, system.ErrNotFound
	}
	return f.gpuUsage, nil
}

func TestRegisterMethodsDispatchesSystemInfo(t *testing.T) {
	fake := &fakeProvider{systemInfo: system.SystemInfo{Hostname: "test-host", OS: "linux"}}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "1", Method: "system.info",
	})
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	var got system.SystemInfo
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Hostname != "test-host" {
		t.Fatalf("Hostname = %q, want %q", got.Hostname, "test-host")
	}
}

func TestRegisterMethodsTranslatesUnavailableError(t *testing.T) {
	fake := &fakeProvider{returnedErr: system.ErrUnavailable}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "2", Method: "system.cpu_usage",
	})
	if resp.Type != fwp.TypeError {
		t.Fatalf("resp.Type = %s, want error", resp.Type)
	}
	if resp.Error.Code != fwp.ErrUnavailable {
		t.Fatalf("resp.Error.Code = %s, want %s", resp.Error.Code, fwp.ErrUnavailable)
	}
}

func TestRegisterMethodsTranslatesNotFoundError(t *testing.T) {
	fake := &fakeProvider{returnedErr: system.ErrNotFound}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "2b", Method: "process.detail",
		Params: json.RawMessage(`{"pid":999}`),
	})
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrNotFound {
		t.Fatalf("resp = %+v, want NOT_FOUND", resp)
	}
}

func TestRegisterMethodsTranslatesOtherErrorsAsInternal(t *testing.T) {
	fake := &fakeProvider{returnedErr: context.DeadlineExceeded}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "3", Method: "system.memory",
	})
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInternal {
		t.Fatalf("resp = %+v, want an INTERNAL error", resp)
	}
}

func TestRegisterMethodsProcessListRoundTrips(t *testing.T) {
	fake := &fakeProvider{processes: []system.ProcessInfo{
		{PID: 1, Name: "init", Status: "running"},
		{PID: 42, Name: "worker", Status: "sleeping"},
	}}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "4", Method: "process.list",
	})
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	var got []system.ProcessInfo
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(got) != 2 || got[1].PID != 42 {
		t.Fatalf("got = %+v, want 2 processes with the second having PID 42", got)
	}
}

func TestRegisterMethodsProcessTreeBuildsFromList(t *testing.T) {
	fake := &fakeProvider{processes: []system.ProcessInfo{
		{PID: 1, PPID: 0, Name: "init"},
		{PID: 2, PPID: 1, Name: "child"},
	}}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "5", Method: "process.tree",
	})
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	var roots []system.ProcessTreeNode
	if err := json.Unmarshal(resp.Result, &roots); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(roots) != 1 || roots[0].PID != 1 {
		t.Fatalf("roots = %+v, want a single root with PID 1", roots)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].PID != 2 {
		t.Fatalf("root's children = %+v, want a single child with PID 2", roots[0].Children)
	}
}

func TestRegisterMethodsProcessDetailRejectsInvalidPID(t *testing.T) {
	fake := &fakeProvider{}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "6", Method: "process.detail",
		Params: json.RawMessage(`{"pid":0}`),
	})
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInvalidParams {
		t.Fatalf("resp = %+v, want INVALID_PARAMS for pid=0", resp)
	}
}

func TestRegisterMethodsProcessTerminatePublishesEventOnSuccess(t *testing.T) {
	fake := &fakeProvider{}
	bus := eventbus.New()
	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeProcessTerminationRequested), 4)
	defer unsub()

	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: bus})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "7", Method: "process.terminate",
		Params: json.RawMessage(`{"pid":123,"force":true}`),
	})
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}

	if len(fake.terminateCalls) != 1 || fake.terminateCalls[0].pid != 123 || !fake.terminateCalls[0].force {
		t.Fatalf("terminateCalls = %+v, want a single call for pid=123 force=true", fake.terminateCalls)
	}

	select {
	case e := <-ch:
		var payload processTerminateEventPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal event payload: %v", err)
		}
		if payload.PID != 123 || !payload.Force || !payload.Success {
			t.Fatalf("payload = %+v, want pid=123 force=true success=true", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TypeProcessTerminationRequested event to be published")
	}
}

func TestRegisterMethodsProcessTerminatePublishesEventOnFailure(t *testing.T) {
	fake := &fakeProvider{returnedErr: system.ErrNotFound}
	bus := eventbus.New()
	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeProcessTerminationRequested), 4)
	defer unsub()

	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: bus})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "8", Method: "process.terminate",
		Params: json.RawMessage(`{"pid":404,"force":false}`),
	})
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrNotFound {
		t.Fatalf("resp = %+v, want NOT_FOUND", resp)
	}

	select {
	case e := <-ch:
		var payload processTerminateEventPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal event payload: %v", err)
		}
		if payload.Success || payload.Error == "" {
			t.Fatalf("payload = %+v, want success=false with a non-empty error", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TypeProcessTerminationRequested event to be published even on failure")
	}
}

func TestRegisterMethodsProcessTerminateRejectsInvalidParams(t *testing.T) {
	fake := &fakeProvider{}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "9", Method: "process.terminate",
		Params: json.RawMessage(`{"pid":-1}`),
	})
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInvalidParams {
		t.Fatalf("resp = %+v, want INVALID_PARAMS for a negative pid", resp)
	}
	if len(fake.terminateCalls) != 0 {
		t.Fatalf("expected TerminateProcess not to be called for invalid params, got %+v", fake.terminateCalls)
	}
}

func TestToFWPErrorNilIsNil(t *testing.T) {
	if toFWPError(nil) != nil {
		t.Fatal("toFWPError(nil) should be nil")
	}
}

func TestRegisterMethodsServiceListRoundTrips(t *testing.T) {
	fake := &fakeProvider{services: []system.ServiceInfo{
		{Name: "sshd.service", State: "running", StartupMode: "enabled"},
	}}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "10", Method: "service.list",
	})
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	var got []system.ServiceInfo
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(got) != 1 || got[0].Name != "sshd.service" {
		t.Fatalf("got = %+v, want a single sshd.service entry", got)
	}
}

func TestRegisterMethodsServiceDetailRejectsEmptyName(t *testing.T) {
	fake := &fakeProvider{}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "11", Method: "service.detail",
		Params: json.RawMessage(`{"name":""}`),
	})
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInvalidParams {
		t.Fatalf("resp = %+v, want INVALID_PARAMS for an empty name", resp)
	}
}

// TestRegisterMethodsContainersActionsRejectEmptyID is Phase 8
// hardening: containers.start/stop/restart previously had no empty-id
// check at all (unlike containers.logs and service.detail, both of
// which already rejected an empty identifier before this) - an empty
// id would have reached containers.Provider and produced a generic
// docker-CLI-usage-error surfaced as an opaque INTERNAL error instead
// of a clear INVALID_PARAMS one. Uses containers.New() (the real
// execRunner) rather than a fake - safe here specifically because the
// validation this test checks for happens before the provider is ever
// called, so the real runner is constructed but never invoked.
func TestRegisterMethodsContainersActionsRejectEmptyID(t *testing.T) {
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: &fakeProvider{}, bus: eventbus.New(), containers: containers.New()})

	for _, method := range []string{"containers.start", "containers.stop", "containers.restart"} {
		resp := router.Dispatch(context.Background(), fwp.Envelope{
			V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "1", Method: method,
			Params: json.RawMessage(`{"id":""}`),
		})
		if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInvalidParams {
			t.Errorf("%s: resp = %+v, want INVALID_PARAMS for an empty id", method, resp)
		}
	}
}

func TestRegisterMethodsServiceStartPublishesEventAndCallsProvider(t *testing.T) {
	fake := &fakeProvider{}
	bus := eventbus.New()
	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeServiceActionRequested), 4)
	defer unsub()

	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: bus})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "12", Method: "service.start",
		Params: json.RawMessage(`{"name":"nginx.service"}`),
	})
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	if fake.serviceActionCall == nil || fake.serviceActionCall.action != "start" || fake.serviceActionCall.name != "nginx.service" {
		t.Fatalf("serviceActionCall = %+v, want start/nginx.service", fake.serviceActionCall)
	}

	select {
	case e := <-ch:
		var payload serviceActionEventPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal event payload: %v", err)
		}
		if payload.Name != "nginx.service" || payload.Action != "start" || !payload.Success {
			t.Fatalf("payload = %+v, want name=nginx.service action=start success=true", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TypeServiceActionRequested event to be published")
	}
}

func TestRegisterMethodsServiceSetEnabledPassesEnabledFlag(t *testing.T) {
	fake := &fakeProvider{}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "13", Method: "service.set_enabled",
		Params: json.RawMessage(`{"name":"nginx.service","enabled":false}`),
	})
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	if fake.serviceActionCall == nil || fake.serviceActionCall.action != "set_enabled" || fake.serviceActionCall.enabled {
		t.Fatalf("serviceActionCall = %+v, want set_enabled with enabled=false", fake.serviceActionCall)
	}
}

func TestRegisterMethodsServiceActionTranslatesUnavailable(t *testing.T) {
	// Mirrors Darwin's real SetServiceEnabled behavior (see
	// provider_darwin.go) - the wiring must surface ErrUnavailable
	// correctly for service actions too, not just for system.* methods.
	fake := &fakeProvider{returnedErr: system.ErrUnavailable}
	router := ipc.NewRouter()
	registerMethods(registration{router: router, sys: fake, bus: eventbus.New()})

	resp := router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "14", Method: "service.set_enabled",
		Params: json.RawMessage(`{"name":"com.apple.something","enabled":true}`),
	})
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrUnavailable {
		t.Fatalf("resp = %+v, want UNAVAILABLE", resp)
	}
}
