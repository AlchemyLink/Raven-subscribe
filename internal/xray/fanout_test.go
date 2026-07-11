package xray

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/core"
)

// fakeAdmin is a scriptable core.AdminAPI for exercising the fanout. It records
// calls and returns canned results/errors.
type fakeAdmin struct {
	stored string // returned by AddClient
	addErr error  // AddClient error
	addEx  error  // AddExistingClient error
	rmErr  error  // RemoveClient error
	addIn  error  // AddInboundFromJSON error
	rmIn   error  // RemoveInbound error

	addCalls    int
	addExCalls  int
	rmCalls     int
	addInCalls  int
	rmInCalls   int
}

func (a *fakeAdmin) AddClient(_, _ string, _ core.AddClientHint) (string, error) {
	a.addCalls++
	return a.stored, a.addErr
}
func (a *fakeAdmin) AddExistingClient(_, _, _ string) error { a.addExCalls++; return a.addEx }
func (a *fakeAdmin) RemoveClient(_, _ string) error         { a.rmCalls++; return a.rmErr }
func (a *fakeAdmin) AddInboundFromJSON(_ string) error      { a.addInCalls++; return a.addIn }
func (a *fakeAdmin) RemoveInbound(_ string) error           { a.rmInCalls++; return a.rmIn }
func (a *fakeAdmin) Engine() string                         { return "fake" }
func (a *fakeAdmin) Version() string                        { return "v-fake" }

func named(name string, a *fakeAdmin) NamedAdmin { return NamedAdmin{Name: name, Admin: a} }

func TestFanout_AddClient_AllSucceed(t *testing.T) {
	a := &fakeAdmin{stored: `{"id":"x"}`}
	b := &fakeAdmin{stored: `{"id":"x"}`}
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", a), named("eu-2", b)})

	got, err := f.AddClient("vless-in", "user@z", core.AddClientHint{})
	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if got != `{"id":"x"}` {
		t.Errorf("stored: got %q", got)
	}
	if a.addCalls != 1 || b.addCalls != 1 {
		t.Errorf("expected fan-out to both nodes: a=%d b=%d", a.addCalls, b.addCalls)
	}
}

func TestFanout_AddClient_PartialFailure_UserStaysOnLiveNode(t *testing.T) {
	live := &fakeAdmin{stored: `{"id":"x"}`}
	down := &fakeAdmin{addErr: fmt.Errorf("connection refused")}
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", live), named("eu-2", down)})

	got, err := f.AddClient("vless-in", "user@z", core.AddClientHint{})
	if err != nil {
		t.Fatalf("partial failure must not fail the op: %v", err)
	}
	if got != `{"id":"x"}` {
		t.Errorf("stored from live node: got %q", got)
	}
	// Fan-out continues past the failure — the down node was still attempted.
	if down.addCalls != 1 {
		t.Errorf("down node should have been attempted, calls=%d", down.addCalls)
	}
}

func TestFanout_AddClient_AllFail_Errors(t *testing.T) {
	a := &fakeAdmin{addErr: fmt.Errorf("boom-a")}
	b := &fakeAdmin{addErr: fmt.Errorf("boom-b")}
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", a), named("eu-2", b)})

	_, err := f.AddClient("vless-in", "user@z", core.AddClientHint{})
	if err == nil {
		t.Fatal("expected error when all nodes fail")
	}
	if !strings.Contains(err.Error(), "eu-1") || !strings.Contains(err.Error(), "eu-2") {
		t.Errorf("error should name failed nodes: %v", err)
	}
}

func TestFanout_AddExistingClient_BenignExistsIsSuccess(t *testing.T) {
	// Xray's real reply is "User X already exists." — the matcher is
	// case-insensitive substring ("exist"), so a lowercased form tests the same.
	a := &fakeAdmin{addEx: fmt.Errorf("user user@z already exists")}
	b := &fakeAdmin{addErr: fmt.Errorf("unused")}
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", a), named("eu-2", b)})

	if err := f.AddExistingClient("vless-in", "user@z", "{}"); err != nil {
		t.Fatalf("benign already-exists must count as success: %v", err)
	}
}

func TestFanout_RemoveClient_BenignNotFoundIsSuccess(t *testing.T) {
	// Xray's real reply is "User X not found." — matched case-insensitively.
	a := &fakeAdmin{rmErr: fmt.Errorf("user user@z not found")}
	b := &fakeAdmin{} // clean remove
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", a), named("eu-2", b)})

	if err := f.RemoveClient("vless-in", "user@z"); err != nil {
		t.Fatalf("benign not-found must count as success: %v", err)
	}
}

func TestFanout_RemoveClient_RealFailureSurfaces(t *testing.T) {
	a := &fakeAdmin{}
	b := &fakeAdmin{rmErr: fmt.Errorf("connection refused")}
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", a), named("eu-2", b)})

	err := f.RemoveClient("vless-in", "user@z")
	if err == nil {
		t.Fatal("expected a real removal failure to surface")
	}
	if !strings.Contains(err.Error(), "eu-2") {
		t.Errorf("error should name the failing node: %v", err)
	}
	// Removal still attempted on every node.
	if a.rmCalls != 1 || b.rmCalls != 1 {
		t.Errorf("remove should fan out to all: a=%d b=%d", a.rmCalls, b.rmCalls)
	}
}

func TestFanout_AddClient_StoredMismatchStillSucceeds(t *testing.T) {
	// Divergent stored configs across nodes are logged as drift but must not
	// break the op — the first success wins.
	a := &fakeAdmin{stored: `{"id":"a"}`}
	b := &fakeAdmin{stored: `{"id":"b"}`}
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", a), named("eu-2", b)})

	got, err := f.AddClient("vless-in", "user@z", core.AddClientHint{})
	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if got != `{"id":"a"}` {
		t.Errorf("first success should win: got %q", got)
	}
}

func TestFanout_Inbound_FanOut(t *testing.T) {
	a := &fakeAdmin{}
	b := &fakeAdmin{}
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", a), named("eu-2", b)})

	if err := f.AddInboundFromJSON(`{"tag":"x"}`); err != nil {
		t.Fatalf("AddInboundFromJSON: %v", err)
	}
	if err := f.RemoveInbound("x"); err != nil {
		t.Fatalf("RemoveInbound: %v", err)
	}
	if a.addInCalls != 1 || b.addInCalls != 1 || a.rmInCalls != 1 || b.rmInCalls != 1 {
		t.Errorf("inbound ops should fan out to all nodes")
	}
}

func TestFanout_Engine(t *testing.T) {
	f := NewFanoutAdmin([]NamedAdmin{named("eu-1", &fakeAdmin{})})
	if f.Engine() != "xray" {
		t.Errorf("Engine: got %q, want xray", f.Engine())
	}
	if f.Version() != "v-fake" {
		t.Errorf("Version should come from first target: got %q", f.Version())
	}
}
