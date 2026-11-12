package serviceprobe

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// scriptedReply pairs one exact closed request with its bounded test response.
type scriptedReply struct {
	request request
	data    []byte
	err     error
}

// scriptedRunner refuses any request outside the expected observation order.
type scriptedRunner struct {
	t       *testing.T
	replies []scriptedReply
	index   int
}

// capture enforces the call deadline and consumes a single exact fixture.
func (s *scriptedRunner) capture(ctx context.Context, r request) ([]byte, error) {
	s.t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > maximumCallDuration || time.Until(deadline) <= 0 {
		s.t.Fatal("query did not retain its bounded active deadline")
	}
	if s.index >= len(s.replies) {
		s.t.Fatal("unexpected additional query")
	}
	reply := s.replies[s.index]
	s.index++
	if reply.request != r {
		s.t.Fatalf("query %d: got %#v, want %#v", s.index, r, reply.request)
	}
	return append([]byte(nil), reply.data...), reply.err
}

// scriptedBoot supplies two boot-ID observations without touching procfs.
type scriptedBoot struct {
	values []string
	index  int
	err    error
}

// read consumes a boot fixture while respecting the enclosing deadline.
func (s *scriptedBoot) read(ctx context.Context) (string, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > maximumObservation {
		return "", errors.New("missing overall deadline")
	}
	if s.err != nil {
		return "", s.err
	}
	if s.index >= len(s.values) {
		return "", errors.New("unexpected boot query")
	}
	value := s.values[s.index]
	s.index++
	return value, nil
}

// observationFixture provides a complete stable manager/proxy query bracket.
func observationFixture(t *testing.T, proxy bool) (*scriptedRunner, *scriptedBoot) {
	t.Helper()
	scalar := func(signature string, value any) []byte { return wireFixture(t, signature, value) }
	replies := []scriptedReply{{request{operation: readBusID}, scalar("s", strings.Repeat("a", 32)), nil}}
	peer := func(presence, owner operation, name string, pid uint32, present bool) {
		replies = append(replies, scriptedReply{request{operation: presence}, scalar("b", present), nil})
		if present {
			replies = append(replies, scriptedReply{request{operation: owner}, scalar("s", name), nil}, scriptedReply{request{operation: peerPID, peer: name}, scalar("u", pid), nil})
		}
	}
	peer(managerPresence, managerOwner, ":1.10", 1, true)
	replies = append(replies, scriptedReply{request{operation: loadedUnits, peer: ":1.10"}, scalar("a(ssssssouso)", []any{unitWireFixture(vendorUnit)}), nil})
	peer(proxyPresence, proxyOwner, ":1.20", 200, proxy)
	peer(managerPresence, managerOwner, ":1.10", 1, true)
	peer(proxyPresence, proxyOwner, ":1.20", 200, proxy)
	replies = append(replies, scriptedReply{request{operation: readBusID}, scalar("s", strings.Repeat("a", 32)), nil})
	boot := "12345678-1234-1234-1234-123456789abc"
	return &scriptedRunner{t: t, replies: replies}, &scriptedBoot{values: []string{boot, boot}}
}

// TestCollectStableDiagnostics verifies exact ordering and defensive facts.
func TestCollectStableDiagnostics(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "proxy-absent", true: "proxy-present"}[present], func(t *testing.T) {
			runner, boot := observationFixture(t, present)
			snapshot, err := collect(context.Background(), runner, boot)
			if err != nil {
				t.Fatal(err)
			}
			facts, err := snapshot.Facts()
			if err != nil || facts.BusID != strings.Repeat("a", 32) || facts.ManagerOwner != ":1.10" || facts.ManagerPID != 1 || len(facts.Units) != 1 {
				t.Fatalf("unexpected facts: %#v, %v", facts, err)
			}
			if present && (facts.ProxyOwner != ":1.20" || facts.ProxyPID != 200) || !present && (facts.ProxyOwner != "" || facts.ProxyPID != 0) {
				t.Fatalf("proxy identity changed: %#v", facts)
			}
			facts.Units[0].Name = "altered.service"
			again, err := snapshot.Facts()
			if err != nil || again.Units[0].Name != vendorUnit || runner.index != len(runner.replies) || boot.index != 2 {
				t.Fatal("facts or observation roster was mutable/incomplete")
			}
		})
	}
	for _, value := range []*Snapshot{nil, {}} {
		if _, err := value.Facts(); err == nil {
			t.Fatal("zero snapshot became valid")
		}
	}
}

// TestCollectErrorsAreNeverAbsence refuses every failed or malformed query.
func TestCollectErrorsAreNeverAbsence(t *testing.T) {
	base, _ := observationFixture(t, true)
	for i := range base.replies {
		t.Run("query-"+string(rune('a'+i)), func(t *testing.T) {
			for _, malformed := range []bool{false, true} {
				runner, boot := observationFixture(t, true)
				if malformed {
					runner.replies[i].data = []byte(`{"type":"s","data":null}`)
				} else {
					runner.replies[i].err = errors.New("private stderr must not escape")
				}
				snapshot, err := collect(context.Background(), runner, boot)
				if err == nil || snapshot != nil || strings.Contains(err.Error(), "private stderr") {
					t.Fatalf("failure became facts or leaked diagnostics: %#v, %v", snapshot, err)
				}
			}
		})
	}
}

// TestCollectRejectsChangedBrackets covers boot, bus, presence, owners and PIDs.
func TestCollectRejectsChangedBrackets(t *testing.T) {
	cases := map[string]func(*scriptedRunner, *scriptedBoot){
		"boot": func(_ *scriptedRunner, b *scriptedBoot) { b.values[1] = "22345678-1234-1234-1234-123456789abc" },
		"bus": func(r *scriptedRunner, _ *scriptedBoot) {
			r.replies[14].data = wireFixture(t, "s", strings.Repeat("b", 32))
		},
		"manager-pid": func(r *scriptedRunner, _ *scriptedBoot) { r.replies[10].data = wireFixture(t, "u", 2) },
		"proxy-pid":   func(r *scriptedRunner, _ *scriptedBoot) { r.replies[13].data = wireFixture(t, "u", 201) },
		"manager-owner": func(r *scriptedRunner, _ *scriptedBoot) {
			r.replies[9].data = wireFixture(t, "s", ":1.11")
			r.replies[10].request.peer = ":1.11"
		},
		"proxy-owner": func(r *scriptedRunner, _ *scriptedBoot) {
			r.replies[12].data = wireFixture(t, "s", ":1.21")
			r.replies[13].request.peer = ":1.21"
		},
		"manager-absence": func(r *scriptedRunner, _ *scriptedBoot) {
			r.replies[8].data = wireFixture(t, "b", false)
			r.replies = append(r.replies[:9], r.replies[11:]...)
		},
		"proxy-absence": func(r *scriptedRunner, _ *scriptedBoot) {
			r.replies[11].data = wireFixture(t, "b", false)
			r.replies = append(r.replies[:12], r.replies[14:]...)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			runner, boot := observationFixture(t, true)
			mutate(runner, boot)
			if snapshot, err := collect(context.Background(), runner, boot); err == nil || snapshot != nil {
				t.Fatal("changed observation bracket became facts")
			}
		})
	}
}

// TestCollectRequiresCanonicalIdentity refuses malformed identities and bounds.
func TestCollectRequiresCanonicalIdentity(t *testing.T) {
	for _, index := range []int{0, 2, 3, 6, 7} {
		runner, boot := observationFixture(t, true)
		switch index {
		case 0:
			runner.replies[index].data = wireFixture(t, "s", strings.Repeat("A", 32))
		case 2, 6:
			runner.replies[index].data = wireFixture(t, "s", managerName)
		default:
			runner.replies[index].data = wireFixture(t, "u", 0)
		}
		if snapshot, err := collect(context.Background(), runner, boot); err == nil || snapshot != nil {
			t.Fatalf("accepted malformed identity at %d", index)
		}
	}
	runner, boot := observationFixture(t, true)
	runner.replies[1].data = wireFixture(t, "b", false)
	if snapshot, err := collect(context.Background(), runner, boot); err == nil || snapshot != nil {
		t.Fatal("accepted manager absence")
	}
	for _, value := range []string{"", "../boot-id", "12345678-1234-1234-1234-123456789ABC", "12345678-1234-1234-1234-123456789abc\n"} {
		runner, boot := observationFixture(t, true)
		boot.values[0] = value
		if snapshot, err := collect(context.Background(), runner, boot); err == nil || snapshot != nil || runner.index != 0 {
			t.Fatal("malformed boot ID reached bus")
		}
	}
	for _, data := range [][]byte{nil, make([]byte, maximumStdout+1)} {
		runner, boot := observationFixture(t, true)
		runner.replies[0].data = data
		if snapshot, err := collect(context.Background(), runner, boot); err == nil || snapshot != nil {
			t.Fatal("unbounded reply became facts")
		}
	}
}

// TestCollectCancellationAndMissingInputs refuses unavailable reader seams.
func TestCollectCancellationAndMissingInputs(t *testing.T) {
	runner, boot := observationFixture(t, true)
	if _, err := collect(nil, runner, boot); err == nil {
		t.Fatal("accepted nil context")
	}
	if _, err := collect(context.Background(), nil, boot); err == nil {
		t.Fatal("accepted nil bus reader")
	}
	if _, err := collect(context.Background(), runner, nil); err == nil {
		t.Fatal("accepted nil boot reader")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collect(ctx, runner, boot); !errors.Is(err, context.Canceled) || runner.index != 0 || boot.index != 0 {
		t.Fatal("cancellation did not stop before observation")
	}
	boot.err = errors.New("boot unavailable")
	if _, err := collect(context.Background(), runner, boot); err == nil || runner.index != 0 {
		t.Fatal("boot read error reached bus")
	}
}

// TestClosedArguments freezes every allowed method and forbids arbitrary peers.
func TestClosedArguments(t *testing.T) {
	prefix := []string{"--system", "--no-pager", "--json=short", "--auto-start=no", "--allow-interactive-authorization=no", "--timeout=2s", "call"}
	cases := []struct {
		request request
		tail    []string
	}{
		{request{operation: readBusID}, []string{busName, busPath, busName, "GetId"}},
		{request{operation: managerPresence}, []string{busName, busPath, busName, "NameHasOwner", "s", managerName}},
		{request{operation: managerOwner}, []string{busName, busPath, busName, "GetNameOwner", "s", managerName}},
		{request{operation: proxyPresence}, []string{busName, busPath, busName, "NameHasOwner", "s", proxyName}},
		{request{operation: proxyOwner}, []string{busName, busPath, busName, "GetNameOwner", "s", proxyName}},
		{request{operation: peerPID, peer: ":1.2"}, []string{busName, busPath, busName, "GetConnectionUnixProcessID", "s", ":1.2"}},
		{request{operation: loadedUnits, peer: ":1.2"}, []string{":1.2", managerPath, managerName + ".Manager", "ListUnitsByPatterns", "asas", "0", "2", vendorUnit, nativeUnit}},
	}
	for _, test := range cases {
		args, err := test.request.arguments()
		want := append(append([]string(nil), prefix...), test.tail...)
		if err != nil || !reflect.DeepEqual(args, want) {
			t.Fatalf("arguments: %#v, %v", args, err)
		}
		args[0] = "--user"
		again, _ := test.request.arguments()
		if again[0] != prefix[0] {
			t.Fatal("argument slice was shared")
		}
	}
	for _, op := range []operation{0, 255, readBusID, managerPresence, managerOwner, proxyPresence, proxyOwner} {
		if _, err := (request{operation: op, peer: ":1.2"}).arguments(); err == nil {
			t.Fatal("accepted unknown operation or unexpected peer")
		}
	}
	for _, peer := range []string{"", managerName, ":1", ":1.2.3", ":1.-1", ":.1", ":1.", ":1.2\n", strings.Repeat("1", 65)} {
		for _, op := range []operation{peerPID, loadedUnits} {
			if _, err := (request{operation: op, peer: peer}).arguments(); err == nil {
				t.Fatalf("accepted arbitrary peer %q", peer)
			}
		}
	}
}
