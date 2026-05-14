package main

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/skills"
)

// package-level lipgloss 样式：在 View() 外定义，避免每帧重复分配。
var (
	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("12")).
			Padding(0, 1)

	userMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9"))

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("11")).
			Padding(0, 1)
)

// eventMsg 将 engine.Event 包装为 tea.Msg，供 Bubbletea 的 Update 分发。
type eventMsg engine.Event

// readNextEvent 返回一个 tea.Cmd，该 Cmd 阻塞直到 ch 中有一个 Event，
// 然后以 eventMsg 形式递交给 Update。ch 关闭时递交 EventDone。
func readNextEvent(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return eventMsg{Type: engine.EventDone}
		}
		return eventMsg(evt)
	}
}

// tuiModel 是 harness9 TUI 的 Bubbletea Elm 模型。
type tuiModel struct {
	// 展示配置（构造时设置，后续不变）
	workDir   string
	modelName string

	// 终端尺寸（由 WindowSizeMsg 更新）
	width, height int

	// Scrollback：所有已渲染行，仅追加
	lines []string

	// Footer 组件
	spinner    spinner.Model
	statusLine string
	input      textinput.Model

	// 当前工具跟踪（用于耗时展示）
	currentTool string

	// 运行时
	eng         *engine.AgentEngine
	skillsIndex *skills.Index
	eventCh     <-chan engine.Event
	cancelFn    context.CancelFunc
	running     bool
}

// newTUIModel 构造已初始化的 tuiModel：输入框聚焦，spinner 使用 Dot 样式。
func newTUIModel(eng *engine.AgentEngine, idx *skills.Index, workDir, modelName string) tuiModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

	ti := textinput.New()
	ti.Placeholder = "输入任务..."
	ti.CharLimit = 0
	ti.Focus()

	return tuiModel{
		workDir:     workDir,
		modelName:   modelName,
		spinner:     sp,
		input:       ti,
		eng:         eng,
		skillsIndex: idx,
	}
}

// Init 实现 tea.Model，启动输入框光标闪烁。
func (m tuiModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update 实现 tea.Model——处理所有消息（Task 3 & 4 完成实现）。
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

// View 实现 tea.Model——渲染完整 TUI 帧（Task 5 完成实现）。
func (m tuiModel) View() string {
	return "harness9 TUI loading...\n"
}

// RunTUI 以 AltScreen 模式启动 Bubbletea 程序（Task 5 完成实现）。
func RunTUI(ctx context.Context, eng *engine.AgentEngine, idx *skills.Index, workDir, modelName string) error {
	return nil
}
