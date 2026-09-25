package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schollz/progressbar/v3"

	"github.com/azzenabidi/acestep-cli/pkg/engine"
)

// EventKind identifies a generation event.
type EventKind int

const (
	// EventStage marks the start of an engine stage.
	EventStage EventKind = iota
	// EventProgress carries a progress observation.
	EventProgress
	// EventTrack announces a finished audio file.
	EventTrack
	// EventDone terminates the run.
	EventDone
)

// Event is a message sent from the engine goroutines to the view.
type Event struct {
	Kind     EventKind
	Stage    engine.Stage
	Label    string
	Track    string
	Tracks   []string
	Detail   string
	Progress engine.Progress
	Elapsed  time.Duration
	Err      error
}

// Reporter fans engine callbacks out to the TUI event channel. The zero value
// is usable and discards everything.
//
// The event channel is never closed. Closing it would race with the engine
// goroutines that are still reporting, so termination is signalled on a
// separate channel that consumers select on instead.
type Reporter struct {
	ch     chan Event
	once   sync.Once
	closed chan struct{}
}

// terminalWait bounds how long a terminal event (a finished track, or the end
// of the stream) waits for room in a full buffer. Without a bound, a consumer
// that has stopped reading would hang the engine goroutine; without enough
// patience, a briefly slow consumer would lose the track.
var terminalWait = 5 * time.Second

// NewReporter creates a reporter with a buffered channel.
func NewReporter() *Reporter {
	return &Reporter{
		ch:     make(chan Event, 256),
		closed: make(chan struct{}),
	}
}

// C is the event channel consumers read from. It stays open after Done; use
// Closed to detect the end of the stream.
func (r *Reporter) C() <-chan Event {
	if r == nil {
		return nil
	}
	return r.ch
}

// Closed is closed when the stream ends.
func (r *Reporter) Closed() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.closed
}

// OnStage implements engine.Options.OnStage.
func (r *Reporter) OnStage(s engine.Stage) {
	r.send(Event{Kind: EventStage, Stage: s})
}

// OnProgress implements engine.Options.OnProgress.
func (r *Reporter) OnProgress(s engine.Stage, p engine.Progress) {
	r.send(Event{Kind: EventProgress, Stage: s, Progress: p})
}

// Track announces a finished file.
func (r *Reporter) Track(path string) {
	r.send(Event{Kind: EventTrack, Track: path})
}

// Done terminates the stream. The engine must not report after this.
func (r *Reporter) Done(tracks []string, elapsed time.Duration, err error) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		// Release any callback already parked on a full buffer before
		// delivering the terminal event, so a slow or absent consumer can
		// never wedge the engine goroutine.
		close(r.closed)
		e := Event{Kind: EventDone, Elapsed: elapsed, Err: err}
		if err == nil {
			e.Tracks = tracks
		}
		timer := time.NewTimer(terminalWait)
		defer timer.Stop()
		select {
		case r.ch <- e:
		case <-timer.C:
		}
	})
}

func (r *Reporter) send(e Event) {
	if r == nil {
		return
	}
	select {
	case r.ch <- e:
		return
	default:
	}
	if e.Kind != EventDone && e.Kind != EventTrack {
		// Progress is the only droppable event. A view that cannot keep up
		// with a chatty engine should lose bar updates, never tracks.
		return
	}
	// A terminal event on a full buffer waits, but not indefinitely: a
	// consumer that has stopped reading must not be able to hang the engine.
	timer := time.NewTimer(terminalWait)
	defer timer.Stop()
	select {
	case r.ch <- e:
	case <-r.closed:
	case <-timer.C:
	}
}

// genModel is the progress view state.
type genModel struct {
	reporter *Reporter
	stages   map[engine.Stage]*stageState
	order    []engine.Stage
	tracks   []string
	err      error
	elapsed  time.Duration
	finished bool
	// lastSeen throttles redraws of percentage-only updates.
	lastSeen map[engine.Stage]time.Time
	quitting bool
	// plain selects the non-TTY renderer.
	plain bool
	out   io.Writer
	quit  atomic.Bool
}

type stageState struct {
	name     string
	fraction float64
	label    string
	started  bool
	done     bool
	start    time.Time
}

// Init subscribes to the event channel.
func (m *genModel) Init() tea.Cmd {
	return waitForEvent(m.reporter)
}

func waitForEvent(r *Reporter) tea.Cmd {
	ch := r.C()
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case e := <-ch:
			return e
		case <-r.Closed():
			// The stream ended, possibly without a final event if the buffer
			// was full. Returning nil tells the view to quit.
			return nil
		}
	}
}

func (m *genModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quit.Store(true)
			return m, tea.Quit
		case tea.KeyRunes:
			if string(msg.Runes) == "q" {
				m.quit.Store(true)
				return m, tea.Quit
			}
		}
	case Event:
		switch msg.Kind {
		case EventStage:
			if s := m.stages[msg.Stage]; s != nil {
				s.started = true
				s.start = time.Now()
				s.label = string(msg.Stage)
			}
		case EventProgress:
			if s := m.stages[msg.Stage]; s != nil {
				s.fraction = msg.Progress.Fraction()
				if msg.Progress.Label != "" {
					s.label = msg.Progress.Label
				}
			}
		case EventTrack:
			m.tracks = append(m.tracks, msg.Track)
		case EventDone:
			m.finished = true
			m.tracks = append(m.tracks, msg.Tracks...)
			m.elapsed = msg.Elapsed
			m.err = msg.Err
			for _, s := range m.stages {
				if s.started {
					s.done = true
					s.fraction = 1
				}
			}
			return m, tea.Quit
		}
		return m, waitForEvent(m.reporter)
	case nil:
		return m, tea.Quit
	}
	return m, waitForEvent(m.reporter)
}

func (m *genModel) View() string {
	if m.finished {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("generating"))
	b.WriteString("\n\n")
	for _, st := range m.order {
		s := m.stages[st]
		switch {
		case !s.started:
			b.WriteString(fmt.Sprintf("  %-10s %s\n", s.name, subtleStyle.Render("pending")))
		case s.done:
			b.WriteString(fmt.Sprintf("  %-10s %s\n", s.name, successStyle.Render("done")))
		default:
			pct := int(s.fraction*100 + 0.5)
			if s.label != "" {
				b.WriteString(fmt.Sprintf("  %-10s %3d%% %s\n", s.name, pct, subtleStyle.Render(s.label)))
			} else {
				b.WriteString(fmt.Sprintf("  %-10s %3d%%\n", s.name, pct))
			}
		}
	}
	b.WriteString("\n  " + subtleStyle.Render("ctrl+c cancel"))
	return b.String()
}

// Plain renders generation progress as simple lines for non-TTY output. It is
// what the CLI falls back to when stdout is redirected or --no-ui is set.
type Plain struct {
	mu     sync.Mutex
	out    io.Writer
	stages map[engine.Stage]*stageState
	order  []engine.Stage
	shown  map[engine.Stage]int
	closed bool
}

// NewPlain creates a line-oriented progress renderer.
func NewPlain(out io.Writer) *Plain {
	return &Plain{
		out:    out,
		stages: map[engine.Stage]*stageState{},
		shown:  map[engine.Stage]int{},
		order:  []engine.Stage{engine.StageLM, engine.StageSynth},
	}
}

// OnStage implements engine.Options.OnStage.
func (p *Plain) OnStage(s engine.Stage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	st := p.stages[s]
	if st == nil {
		st = &stageState{name: string(s)}
		p.stages[s] = st
	}
	st.started = true
	st.start = time.Now()
	fmt.Fprintf(p.out, "%s: planning lyrics and audio codes...\n", s)
}

// OnProgress implements engine.Options.OnProgress.
func (p *Plain) OnProgress(s engine.Stage, pr engine.Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	st := p.stages[s]
	if st == nil {
		st = &stageState{name: string(s), started: true}
		p.stages[s] = st
	}
	st.fraction = pr.Fraction()
	if pr.Label != "" {
		st.label = pr.Label
	}
	pct := int(st.fraction*100 + 0.5)
	// Only rewrite the line when the percentage actually moves, so redirected
	// output stays readable instead of filling with carriage returns.
	if p.shown[s] != pct {
		p.shown[s] = pct
		fmt.Fprintf(p.out, "%s: %d%%\n", s, pct)
	}
}

// Done closes the renderer.
func (p *Plain) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for _, s := range p.order {
		if st, ok := p.stages[s]; ok && st.started && !st.done {
			st.done = true
		}
	}
}

// RunGenerationView drives the Bubbletea progress view until the reporter
// stream closes. safeFallback is used when the terminal cannot host a
// full-screen program (no TTY, dumb terminal, or --no-ui).
func RunGenerationView(ctx context.Context, r *Reporter, safeFallback func() error) error {
	if safeFallback != nil {
		return safeFallback()
	}
	m := &genModel{
		reporter: r,
		stages:   map[engine.Stage]*stageState{},
		lastSeen: map[engine.Stage]time.Time{},
		order:    []engine.Stage{engine.StageLM, engine.StageSynth},
	}
	for _, s := range m.order {
		m.stages[s] = &stageState{name: string(s)}
	}
	// A cancelled context must be able to tear the program down.
	doneCh := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			m.quit.Store(true)
			if p := lastProgram.Load(); p != nil {
				p.Quit()
			}
		case <-doneCh:
		}
	}()

	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	lastProgram.Store(p)
	_, err := p.Run()
	close(doneCh)
	lastProgram.Store(nil)
	return err
}

var lastProgram = &programHolder{}

type programHolder struct {
	mu sync.Mutex
	p  *tea.Program
}

func (h *programHolder) Store(p *tea.Program) {
	h.mu.Lock()
	h.p = p
	h.mu.Unlock()
}

func (h *programHolder) Load() *tea.Program {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.p
}

// Bar renders a single static progress bar, used for single-shot downloads
// outside the multi-stage view.
func Bar(total int64, label string) *progressbar.ProgressBar {
	return progressbar.NewOptions64(
		total,
		progressbar.OptionSetDescription(label),
		progressbar.OptionSetWriter(os.Stdout),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(32),
		progressbar.OptionThrottle(120*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionOnCompletion(func() { fmt.Fprintln(os.Stdout) }),
	)
}
