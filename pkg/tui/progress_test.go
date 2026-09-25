package tui

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azzenabidi/acestep-cli/pkg/engine"
	tea "github.com/charmbracelet/bubbletea"
)

// collect drains a reporter until the stream ends, mirroring what the view does
// with C and Closed.
func collect(t *testing.T, r *Reporter) []Event {
	t.Helper()
	var got []Event
	for {
		select {
		case e := <-r.C():
			got = append(got, e)
		case <-r.Closed():
			// Drain anything already buffered before reporting the end.
			for {
				select {
				case e := <-r.C():
					got = append(got, e)
				default:
					return got
				}
			}
		}
	}
}

// The blocked-send cases exist to prove the wait is bounded, so shrink the
// bound rather than making the suite sit through the production value.
func TestMain(m *testing.M) {
	terminalWait = 200 * time.Millisecond
	os.Exit(m.Run())
}

func TestReporterForwardsEngineCallbacks(t *testing.T) {
	r := NewReporter()

	r.OnStage(engine.StageLM)
	r.OnProgress(engine.StageLM, engine.Progress{Percent: 42})
	r.Track("/tmp/a.mp3")
	r.Done([]string{"/tmp/a.mp3"}, 3*time.Second, nil)

	got := collect(t, r)
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4: %+v", len(got), got)
	}
	if got[0].Kind != EventStage || got[0].Stage != engine.StageLM {
		t.Errorf("first event = %+v, want stage lm", got[0])
	}
	if got[1].Kind != EventProgress || got[1].Progress.Percent != 42 {
		t.Errorf("second event = %+v, want progress 42", got[1])
	}
	if got[2].Track != "/tmp/a.mp3" {
		t.Errorf("third event = %+v, want the track path", got[2])
	}
	if got[3].Kind != EventDone || got[3].Elapsed != 3*time.Second || got[3].Err != nil {
		t.Errorf("final event = %+v, want a clean done", got[3])
	}
	if len(got[3].Tracks) != 1 || got[3].Tracks[0] != "/tmp/a.mp3" {
		t.Errorf("done event tracks = %v, want the finished path", got[3].Tracks)
	}
}

// A second Done must be a no-op: it would otherwise panic on a closed channel.
func TestReporterDoneIsIdempotent(t *testing.T) {
	r := NewReporter()
	r.Done(nil, time.Second, nil)
	r.Done(nil, time.Second, nil)

	var dones int
	for _, e := range collect(t, r) {
		if e.Kind == EventDone {
			dones++
		}
	}
	if dones != 1 {
		t.Errorf("got %d done events, want 1", dones)
	}
}

// Reporting after Done must be dropped rather than panic or block, because the
// engine's cleanup path can report after the view has torn down.
func TestReporterSendAfterDoneDoesNotPanic(t *testing.T) {
	r := NewReporter()
	r.Done(nil, time.Second, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			r.OnStage(engine.StageSynth)
			r.OnProgress(engine.StageSynth, engine.Progress{Percent: float64(i)})
			r.Track("/tmp/late.mp3")
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("reporting after Done blocked")
	}
}

// Progress is the only droppable event; stage transitions and finished tracks
// must survive a consumer that is merely slow.
func TestReporterNeverDropsTerminalEvents(t *testing.T) {
	r := NewReporter()

	var tracks, dones int
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for _, e := range collect(t, r) {
			switch e.Kind {
			case EventTrack:
				tracks++
			case EventDone:
				dones++
			}
		}
	}()

	// Overflow the buffer with progress, which is allowed to be discarded.
	for i := 0; i < 1000; i++ {
		r.OnProgress(engine.StageLM, engine.Progress{Percent: float64(i % 100)})
	}
	r.Track("/tmp/important.mp3")
	r.Done([]string{"/tmp/important.mp3"}, time.Second, nil)

	<-drained
	if tracks != 1 || dones != 1 {
		t.Errorf("got %d track and %d done events, want 1 and 1", tracks, dones)
	}
}

// A consumer that has stopped reading must not be able to wedge the engine:
// that is what happens when the user presses Ctrl+C and the view quits while
// the engine is still reporting. Done has to release anything already blocked.
func TestDoneReleasesACallbackBlockedOnAFullBuffer(t *testing.T) {
	r := NewReporter()
	// Fill the buffer without reading, so the next terminal event has to wait.
	for i := 0; i < cap(r.ch); i++ {
		r.OnProgress(engine.StageLM, engine.Progress{Percent: 1})
	}

	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		r.Track("/tmp/orphan.mp3")
	}()

	// Give the blocked send time to park, then terminate the stream.
	time.Sleep(50 * time.Millisecond)
	r.Done(nil, time.Second, nil)

	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("Done did not release a callback blocked on a full buffer")
	}
}

// Done must not itself hang when nothing is reading the buffer.
func TestDoneDoesNotBlockOnAnAbandonedConsumer(t *testing.T) {
	r := NewReporter()
	for i := 0; i < cap(r.ch); i++ {
		r.OnProgress(engine.StageLM, engine.Progress{Percent: 1})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Done([]string{"/tmp/lost.mp3"}, time.Second, nil)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Done blocked behind an abandoned consumer")
	}
}

func TestNilReporterIsSafe(t *testing.T) {
	var r *Reporter
	r.OnStage(engine.StageLM)
	r.OnProgress(engine.StageLM, engine.Progress{Percent: 1})
	r.Track("/tmp/x.mp3")
	r.Done(nil, 0, nil)
	if r.C() != nil || r.Closed() != nil {
		t.Error("a nil reporter should expose nil channels")
	}
}

func TestPlainPrintsALineOnlyWhenThePercentageMoves(t *testing.T) {
	var buf bytes.Buffer
	p := NewPlain(&buf)

	p.OnStage(engine.StageLM)
	p.OnProgress(engine.StageLM, engine.Progress{Percent: 0})
	p.OnProgress(engine.StageLM, engine.Progress{Percent: 0.001})
	p.OnProgress(engine.StageLM, engine.Progress{Percent: 50})
	p.OnProgress(engine.StageLM, engine.Progress{Percent: 50})
	p.OnProgress(engine.StageLM, engine.Progress{Percent: 100})
	p.Done()

	out := buf.String()
	if got := strings.Count(out, "50%"); got != 1 {
		t.Errorf("50%% printed %d times, want 1:\n%s", got, out)
	}
	if got := strings.Count(out, "100%"); got != 1 {
		t.Errorf("100%% printed %d times, want 1:\n%s", got, out)
	}
	// A leading 0% adds nothing after the stage line and is not worth a line.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, ": 0%") {
			t.Errorf("0%% should not produce a line of its own:\n%s", out)
		}
	}
}

func TestPlainIgnoresEventsAfterDone(t *testing.T) {
	var buf bytes.Buffer
	p := NewPlain(&buf)
	p.OnStage(engine.StageLM)
	p.Done()
	p.OnProgress(engine.StageLM, engine.Progress{Percent: 90})
	p.OnStage(engine.StageSynth)

	if strings.Contains(buf.String(), "90%") || strings.Contains(buf.String(), "synth") {
		t.Errorf("output after Done should be suppressed:\n%s", buf.String())
	}
}

func TestPlainIsSafeUnderConcurrentCallbacks(t *testing.T) {
	var buf bytes.Buffer
	p := NewPlain(&buf)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.OnStage(engine.StageLM)
			for n := 0; n < 50; n++ {
				p.OnProgress(engine.StageLM, engine.Progress{Percent: float64(n * 2)})
			}
			p.Done()
		}(i)
	}
	wg.Wait()
}

func TestGenModelRendersStageStates(t *testing.T) {
	r := NewReporter()
	m := &genModel{
		reporter: r,
		stages:   map[engine.Stage]*stageState{},
		lastSeen: map[engine.Stage]time.Time{},
		order:    []engine.Stage{engine.StageLM, engine.StageSynth},
	}
	for _, s := range m.order {
		m.stages[s] = &stageState{name: string(s)}
	}

	if view := m.View(); !strings.Contains(view, "pending") {
		t.Errorf("before any stage starts both should be pending:\n%s", view)
	}

	m.Update(Event{Kind: EventStage, Stage: engine.StageLM})
	m.Update(Event{Kind: EventProgress, Stage: engine.StageLM, Progress: engine.Progress{Percent: 60}})

	view := m.View()
	if !strings.Contains(view, "60%") {
		t.Errorf("view should show 60%% for ace-lm:\n%s", view)
	}

	m.Update(Event{Kind: EventDone, Tracks: []string{"/tmp/a.mp3"}, Elapsed: time.Second})
	if m.finished {
		if view := m.View(); view != "" {
			t.Errorf("a finished model should render nothing, got:\n%s", view)
		}
	}
}

func TestGenModelQuitsOnCancelKeys(t *testing.T) {
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("q")},
	} {
		m := &genModel{
			reporter: NewReporter(),
			stages:   map[engine.Stage]*stageState{},
			lastSeen: map[engine.Stage]time.Time{},
		}
		_, cmd := m.Update(msg)
		if !m.quit.Load() {
			t.Errorf("key %v should set quit", msg.Type)
		}
		if cmd == nil {
			t.Errorf("key %v should return tea.Quit", msg.Type)
		}
	}
}
