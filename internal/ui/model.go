// Package ui implements the Bubble Tea terminal interface: three tabs (Ping,
// Route, Speed) fed by snapshots that the probe layer pushes over channels.
package ui

import (
	"context"
	"time"

	"github.com/tavvet/net-test/internal/probe"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type tab int

const (
	tabPing tab = iota
	tabTrace
	tabSpeed
	tabCount
)

var tabNames = []string{"Пинг", "Маршрут", "Скорость"}

// Channels carries the live data streams from the probe layer into the UI.
type Channels struct {
	Ping  chan probe.PingStats
	Trace chan probe.TraceSnapshot
	Speed chan probe.SpeedProgress
}

type model struct {
	ctx     context.Context
	ch      Channels
	tab     tab
	w, h    int
	target  string
	ip      string
	started time.Time

	ping      probe.PingStats
	havePing  bool
	trace     probe.TraceSnapshot
	haveTrace bool
	speed     probe.SpeedProgress
	speedRun  bool

	spin spinner.Model
	quit bool
}

// Messages wrapping channel reads.
type pingMsg probe.PingStats
type traceMsg probe.TraceSnapshot
type speedMsg probe.SpeedProgress

// New builds the root model.
func New(ctx context.Context, target, ip string, ch Channels, started time.Time) tea.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle
	return model{ctx: ctx, ch: ch, target: target, ip: ip, started: started, spin: s}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, waitPing(m.ch.Ping), waitTrace(m.ch.Trace), waitSpeed(m.ch.Speed))
}

func waitPing(ch chan probe.PingStats) tea.Cmd {
	return func() tea.Msg {
		v, ok := <-ch
		if !ok {
			return nil
		}
		return pingMsg(v)
	}
}

func waitTrace(ch chan probe.TraceSnapshot) tea.Cmd {
	return func() tea.Msg {
		v, ok := <-ch
		if !ok {
			return nil
		}
		return traceMsg(v)
	}
}

func waitSpeed(ch chan probe.SpeedProgress) tea.Cmd {
	return func() tea.Msg {
		v, ok := <-ch
		if !ok {
			return nil
		}
		return speedMsg(v)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case pingMsg:
		m.ping, m.havePing = probe.PingStats(msg), true
		return m, waitPing(m.ch.Ping)

	case traceMsg:
		m.trace, m.haveTrace = probe.TraceSnapshot(msg), true
		return m, waitTrace(m.ch.Trace)

	case speedMsg:
		m.speed = probe.SpeedProgress(msg)
		if m.speed.Phase == probe.PhaseDone || m.speed.Phase == probe.PhaseError {
			m.speedRun = false
		}
		return m, waitSpeed(m.ch.Speed)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quit = true
		return m, tea.Quit
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % tabCount
	case "shift+tab", "left", "h":
		m.tab = (m.tab + tabCount - 1) % tabCount
	case "1":
		m.tab = tabPing
	case "2":
		m.tab = tabTrace
	case "3":
		m.tab = tabSpeed
	case "s", "enter":
		if !m.speedRun {
			m.speedRun = true
			m.tab = tabSpeed
			go probe.RunSpeedTest(m.ctx, m.ch.Speed)
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.quit {
		return "Пока!\n"
	}
	if m.w == 0 {
		return "Запуск…\n"
	}
	return m.render()
}
