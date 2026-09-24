package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"class-calendar-adder/internal/domain"
	"class-calendar-adder/internal/usecase"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tuiScreen int

const (
	screenCourses tuiScreen = iota
	screenCourseForm
	screenSettings
	screenExceptions
	screenReview
	screenRegistering
	screenResults
	screenImport
)

type period struct{ Start, End string }

var defaultPeriods = []period{{"08:40", "10:20"}, {"10:35", "12:15"}, {"13:15", "14:55"}, {"15:10", "16:50"}, {"17:05", "18:45"}}

type calendarSettings struct {
	StartDate string   `json:"-"` // Runtime-only: the semester is selected per run.
	EndDate   string   `json:"-"` // Runtime-only: the semester is selected per run.
	Periods   []period `json:"periods"`
}

type courseInput struct {
	Term     string
	Title    string
	Weekday  string
	Period   string
	Teacher  string
	Location string
	Note     string
}

type exceptionSettings struct {
	SkipWeekends   bool
	Holidays       string // YYYY-MM-DD,YYYY-MM-DD
	WeekdayChanges string // destination YYYY-MM-DD=source weekday, ...
}

type registrationEvent struct {
	completed int
	total     int
	current   string
	result    *usecase.AddResult
	done      bool
	results   []usecase.AddResult
}

type registrationEventMsg struct {
	event registrationEvent
	ch    <-chan registrationEvent
}

type exportCourse struct {
	Term     string `json:"term"`
	Title    string `json:"title"`
	Weekday  string `json:"weekday"`
	Period   string `json:"period"`
	Teacher  string `json:"teacher"`
	Location string `json:"location,omitempty"`
	Note     string `json:"note,omitempty"`
}

type exportDocument struct {
	SchemaVersion  int            `json:"schema_version"`
	ExportedAt     string         `json:"exported_at"`
	CalendarID     string         `json:"calendar_id"`
	TimeZone       string         `json:"timezone"`
	StartDate      string         `json:"start_date"`
	EndDate        string         `json:"end_date"`
	Periods        []period       `json:"periods"`
	SkipWeekends   bool           `json:"skip_weekends"`
	Holidays       []string       `json:"holidays,omitempty"`
	WeekdayChanges []string       `json:"weekday_changes,omitempty"`
	Courses        []exportCourse `json:"courses"`
}

type calendarModel struct {
	screen, previousScreen tuiScreen
	adder                  usecase.LessonAdder
	calendarID             string
	timeZone               string
	settingsPath           string
	exportPath             string
	importPath             string
	settings               calendarSettings
	exceptions             exceptionSettings
	courses                []courseInput
	selected, editing      int
	inputs                 []textinput.Model
	errorMsg               string
	statusMsg              string
	results                []usecase.AddResult
	retryLessons           []domain.Lesson
	progressResults        []usecase.AddResult
	progressCompleted      int
	progressTotal          int
	progressCurrent        string
	width                  int
	height                 int
}

var (
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	goodStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	badStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(0, 1)
)

func loadSettings(path string) calendarSettings {
	settings := calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if len(settings.Periods) != 5 {
		settings.Periods = append([]period(nil), defaultPeriods...)
	}
	if settings.StartDate == "" {
		settings.StartDate = "2026-04-01"
	}
	if settings.EndDate == "" {
		settings.EndDate = "2026-07-31"
	}
	return settings
}

func saveSettings(path string, settings calendarSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func newCalendarModel(adder usecase.LessonAdder, timeZone, settingsPath string, settings calendarSettings) calendarModel {
	return calendarModel{adder: adder, calendarID: "primary", timeZone: timeZone, settingsPath: settingsPath, settings: settings, exceptions: exceptionSettings{SkipWeekends: true}, editing: -1}
}

func (m calendarModel) Init() tea.Cmd { return nil }

func (m calendarModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if progress, ok := msg.(registrationEventMsg); ok {
		if progress.event.done {
			m.results, m.screen, m.errorMsg = progress.event.results, screenResults, ""
			m.retryLessons = m.retryLessons[:0]
			for _, item := range progress.event.results {
				if item.Err != nil {
					m.retryLessons = append(m.retryLessons, item.Lesson)
				}
			}
			return m, nil
		}
		m.progressCompleted = progress.event.completed
		m.progressTotal = progress.event.total
		m.progressCurrent = progress.event.current
		if progress.event.result != nil {
			m.progressResults = append(m.progressResults, *progress.event.result)
		}
		return m, waitRegistrationEvent(progress.ch)
	}
	switch m.screen {
	case screenCourses:
		return m.updateCourses(msg)
	case screenCourseForm:
		return m.updateCourseForm(msg)
	case screenSettings, screenExceptions:
		return m.updateSimpleForm(msg)
	case screenReview:
		return m.updateReview(msg)
	case screenRegistering:
		return m, nil
	case screenResults:
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "r":
				if len(m.retryLessons) > 0 {
					lessons := append([]domain.Lesson(nil), m.retryLessons...)
					return m, m.beginRegistration(lessons)
				}
			case "enter", "b":
				m.screen, m.errorMsg = screenCourses, ""
				return m, nil
			case "q":
				return m, tea.Quit
			}
		}
	case screenImport:
		return m.updateImportForm(msg)
	}
	return m, nil
}

func (m calendarModel) updateCourses(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(m.courses)-1 {
			m.selected++
		}
	case "n", "a":
		m.openCourseForm(-1)
	case "e":
		if len(m.courses) > 0 {
			m.openCourseForm(m.selected)
		}
	case "d", "delete":
		if len(m.courses) > 0 {
			m.courses = append(m.courses[:m.selected], m.courses[m.selected+1:]...)
			if m.selected >= len(m.courses) && m.selected > 0 {
				m.selected--
			}
		}
	case "s":
		m.openSettingsForm()
	case "x":
		m.openExceptionsForm()
	case "E":
		if err := m.exportConfiguration(); err != nil {
			m.errorMsg, m.statusMsg = "", "エクスポート失敗: "+err.Error()
		} else {
			m.errorMsg = ""
		}
	case "I":
		m.openImportForm()
	case "r":
		if len(m.courses) == 0 {
			m.errorMsg = "授業を1件以上追加してください"
			return m, nil
		}
		if _, err := m.buildLessons(); err != nil {
			m.errorMsg = err.Error()
			return m, nil
		}
		m.errorMsg, m.screen = "", screenReview
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m *calendarModel) openCourseForm(index int) {
	m.screen, m.editing, m.errorMsg = screenCourseForm, index, ""
	value := courseInput{Term: "全期", Weekday: "月", Period: "1"}
	if index >= 0 {
		value = m.courses[index]
	}
	labels := []string{"期間種別（j/kで選択）", "授業名", "曜日（j/kで選択）", "時限 (1-5)", "教師名", "教室", "メモ"}
	values := []string{value.Term, value.Title, value.Weekday, value.Period, value.Teacher, value.Location, value.Note}
	m.inputs = makeInputs(labels, values)
}

func (m *calendarModel) openSettingsForm() {
	m.previousScreen, m.screen, m.errorMsg = screenCourses, screenSettings, ""
	labels, values := make([]string, 7), make([]string, 7)
	labels[0], labels[1] = "授業開始日 (YYYY-MM-DD)", "授業終了日 (YYYY-MM-DD)"
	values[0], values[1] = m.settings.StartDate, m.settings.EndDate
	for i, p := range m.settings.Periods {
		labels[i+2] = fmt.Sprintf("%d限 (HH:MM-HH:MM)", i+1)
		values[i+2] = p.Start + "-" + p.End
	}
	m.inputs = makeInputs(labels, values)
}

func (m *calendarModel) openExceptionsForm() {
	m.previousScreen, m.screen, m.errorMsg = screenCourses, screenExceptions, ""
	m.inputs = makeInputs([]string{"休講日 (YYYY-MM-DD, ...)", "曜日変更 (変更先日付=変更元曜日, ...)"}, []string{m.exceptions.Holidays, m.exceptions.WeekdayChanges})
}

func (m *calendarModel) openImportForm() {
	m.previousScreen, m.screen, m.errorMsg = screenCourses, screenImport, ""
	path := m.importPath
	if path == "" {
		path = m.exportPath
	}
	if path == "" {
		path = latestExportPath()
	}
	if path == "" {
		path = "class-calendar-export.json"
	}
	m.inputs = makeInputs([]string{"インポートファイル (JSON)"}, []string{path})
}

func latestExportPath() string {
	matches, err := filepath.Glob("class-calendar-export*.json")
	if err != nil {
		return ""
	}
	var latest string
	var latestTime time.Time
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if latest == "" || info.ModTime().After(latestTime) {
			latest, latestTime = path, info.ModTime()
		}
	}
	return latest
}

func makeInputs(labels, values []string) []textinput.Model {
	inputs := make([]textinput.Model, len(labels))
	for i := range labels {
		input := textinput.New()
		input.Prompt, input.CharLimit, input.Placeholder = "", 240, labels[i]
		input.SetValue(values[i])
		inputs[i] = input
	}
	inputs[0].Focus()
	return inputs
}

func (m calendarModel) updateCourseForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && m.focused() == 0 {
		switch key.String() {
		case "j", "h":
			m.changeTerm(-1)
			return m, nil
		case "k", "l":
			m.changeTerm(1)
			return m, nil
		case "tab", "enter", "shift+tab", "up", "down", "[A", "[B":
			// Let shared form navigation handle these keys.
		default:
			return m, nil
		}
	}
	if key, ok := msg.(tea.KeyMsg); ok && m.focused() == 2 {
		switch key.String() {
		case "j", "h":
			m.changeWeekday(-1)
			return m, nil
		case "k", "l":
			m.changeWeekday(1)
			return m, nil
		case "tab", "enter", "shift+tab":
			// Let the shared form navigation handle these keys.
		case "up", "down", "left", "right", "[A", "[B", "[C", "[D":
			// Arrow keys are reserved for moving between fields/cursors.
		default:
			// Weekday is a selector, not a free-text field.
			return m, nil
		}
	}
	return m.updateInputs(msg, func(values []string) (tea.Model, tea.Cmd) {
		value := courseInput{Term: values[0], Title: values[1], Weekday: values[2], Period: values[3], Teacher: values[4], Location: values[5], Note: values[6]}
		if value.Title == "" {
			m.errorMsg = "授業名は必須です"
			return m, nil
		}
		if _, err := m.courseLessons(value); err != nil {
			m.errorMsg = err.Error()
			return m, nil
		}
		if m.editing >= 0 {
			m.courses[m.editing] = value
		} else {
			m.courses = append(m.courses, value)
			m.selected = len(m.courses) - 1
		}
		m.screen, m.errorMsg = screenCourses, ""
		return m, nil
	})
}

var weekdayChoices = []string{"月", "火", "水", "木", "金"}
var termChoices = []string{"全期", "半期"}

func (m *calendarModel) changeWeekday(direction int) {
	if len(m.inputs) < 3 {
		return
	}
	value := m.inputs[2].Value()
	index := 0
	for i, weekday := range weekdayChoices {
		if value == weekday {
			index = i
			break
		}
	}
	index = (index + direction + len(weekdayChoices)) % len(weekdayChoices)
	m.inputs[2].SetValue(weekdayChoices[index])
}

func (m *calendarModel) changeTerm(direction int) {
	if len(m.inputs) == 0 {
		return
	}
	index := 0
	for i, term := range termChoices {
		if m.inputs[0].Value() == term {
			index = i
			break
		}
	}
	index = (index + direction + len(termChoices)) % len(termChoices)
	m.inputs[0].SetValue(termChoices[index])
}

func (m calendarModel) updateSimpleForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.screen == screenExceptions {
		if key, ok := msg.(tea.KeyMsg); ok && (key.Type == tea.KeySpace || key.String() == " ") {
			m.exceptions.SkipWeekends = !m.exceptions.SkipWeekends
			return m, nil
		}
	}
	return m.updateInputs(msg, func(values []string) (tea.Model, tea.Cmd) {
		if m.screen == screenSettings {
			startDate, startErr := time.Parse("2006-01-02", values[0])
			endDate, endErr := time.Parse("2006-01-02", values[1])
			if startErr != nil || endErr != nil {
				m.errorMsg = "開始日・終了日は YYYY-MM-DD 形式で入力してください"
				return m, nil
			}
			if endDate.Before(startDate) {
				m.errorMsg = "終了日は開始日以降にしてください"
				return m, nil
			}
			periods := make([]period, 5)
			for i, value := range values[2:] {
				parts := strings.Split(value, "-")
				if len(parts) != 2 {
					m.errorMsg = fmt.Sprintf("%d限は HH:MM-HH:MM 形式で入力してください", i+1)
					return m, nil
				}
				if _, _, err := parsePeriod(parts[0], parts[1]); err != nil {
					m.errorMsg = fmt.Sprintf("%d限: %v", i+1, err)
					return m, nil
				}
				periods[i] = period{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])}
			}
			m.settings.StartDate, m.settings.EndDate, m.settings.Periods = values[0], values[1], periods
			if err := saveSettings(m.settingsPath, m.settings); err != nil {
				m.errorMsg = "設定を保存できません: " + err.Error()
				return m, nil
			}
		} else {
			if err := validateExceptions(values[0], values[1]); err != nil {
				m.errorMsg = err.Error()
				return m, nil
			}
			m.exceptions.Holidays, m.exceptions.WeekdayChanges = values[0], values[1]
		}
		m.screen, m.errorMsg = screenCourses, ""
		return m, nil
	})
}

func (m calendarModel) updateImportForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.updateInputs(msg, func(values []string) (tea.Model, tea.Cmd) {
		if values[0] == "" {
			m.errorMsg = "インポートファイルのパスを入力してください"
			return m, nil
		}
		if err := m.importConfiguration(values[0]); err != nil {
			m.errorMsg = "インポート失敗: " + err.Error()
			return m, nil
		}
		m.importPath, m.screen, m.errorMsg = values[0], screenCourses, ""
		m.statusMsg = "インポートしました: " + values[0]
		return m, nil
	})
}

func (m calendarModel) updateInputs(msg tea.Msg, save func([]string) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.screen, m.errorMsg = screenCourses, ""
			return m, nil
		case "tab", "enter":
			if m.focused() == len(m.inputs)-1 && key.String() == "enter" {
				return save(inputValues(m.inputs))
			}
			m.focusNext(1)
			return m, nil
		case "up", "[A":
			m.focusNext(-1)
			return m, nil
		case "down", "[B":
			m.focusNext(1)
			return m, nil
		case "shift+tab":
			m.focusNext(-1)
			return m, nil
		case "[C", "[D":
			// Some terminals split an arrow escape sequence. Do not insert
			// the trailing A/B/C/D bytes into Japanese text fields.
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.inputs[m.focused()], cmd = m.inputs[m.focused()].Update(msg)
	return m, cmd
}

func inputValues(inputs []textinput.Model) []string {
	values := make([]string, len(inputs))
	for i := range inputs {
		values[i] = strings.TrimSpace(inputs[i].Value())
	}
	return values
}
func (m *calendarModel) focused() int {
	for i := range m.inputs {
		if m.inputs[i].Focused() {
			return i
		}
	}
	return 0
}
func (m *calendarModel) focusNext(direction int) {
	current := m.focused()
	m.inputs[current].Blur()
	m.inputs[(current+direction+len(m.inputs))%len(m.inputs)].Focus()
}

func (m calendarModel) updateReview(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc", "b":
		m.screen = screenCourses
	case "enter":
		lessons, err := m.buildLessons()
		if err != nil {
			m.errorMsg, m.screen = err.Error(), screenCourses
			return m, nil
		}
		return m, m.beginRegistration(lessons)
	}
	return m, nil
}

func (m *calendarModel) beginRegistration(lessons []domain.Lesson) tea.Cmd {
	m.screen = screenRegistering
	m.errorMsg = ""
	m.progressResults = nil
	m.progressCompleted = 0
	m.progressTotal = len(lessons)
	m.progressCurrent = "登録を準備しています"
	ch := make(chan registrationEvent)
	go func() {
		results := m.adder.AddAllProgress(lessons, func(completed, total int, result usecase.AddResult) {
			ch <- registrationEvent{completed: completed, total: total, current: result.Lesson.Title, result: &result}
		})
		ch <- registrationEvent{completed: len(results), total: len(results), done: true, results: results}
		close(ch)
	}()
	return waitRegistrationEvent(ch)
}

func waitRegistrationEvent(ch <-chan registrationEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil
		}
		return registrationEventMsg{event: event, ch: ch}
	}
}

func (m calendarModel) View() string {
	header := accentStyle.Render("CLASS CALENDAR ADDER") + mutedStyle.Render("  Google Calendar")
	var content string
	switch m.screen {
	case screenCourses:
		content = m.coursesView()
	case screenCourseForm:
		content = m.inputsView("授業を入力", []string{"期間種別（j/kで選択）", "授業名", "曜日（j/kで選択）", "時限 (1-5)", "教師名", "教室", "メモ"})
	case screenSettings:
		content = m.inputsView("授業期間・時限設定", []string{"授業開始日 (YYYY-MM-DD)", "授業終了日 (YYYY-MM-DD)", "1限 (HH:MM-HH:MM)", "2限 (HH:MM-HH:MM)", "3限 (HH:MM-HH:MM)", "4限 (HH:MM-HH:MM)", "5限 (HH:MM-HH:MM)"})
	case screenExceptions:
		content = m.exceptionsView()
	case screenReview:
		content = m.reviewView()
	case screenRegistering:
		content = m.registrationView()
	case screenResults:
		content = m.resultsView()
	case screenImport:
		content = m.inputsView("インポート", []string{"インポートファイル (JSON)"})
	}
	return header + "\n\n" + content + "\n\n" + mutedStyle.Render("タイムゾーン: "+m.timeZone)
}

func (m calendarModel) coursesView() string {
	lines := []string{accentStyle.Render("授業一覧"), mutedStyle.Render("n:追加  e:編集  d:削除  s:時限設定  x:休講/曜日変更  I:インポート  E:エクスポート  r:確認・登録  q:終了"), ""}
	if len(m.courses) == 0 {
		lines = append(lines, mutedStyle.Render("まだ授業がありません。nキーで追加してください。"))
	}
	for i, course := range m.courses {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		teacher := ""
		if course.Teacher != "" {
			teacher = "  / " + course.Teacher
		}
		lines = append(lines, prefix+course.Title+teacher+"  ["+course.Term+"] "+course.Weekday+" "+course.Period+"限  "+periodText(m.settings, course.Period))
	}
	lines = append(lines, "", mutedStyle.Render(fmt.Sprintf("共通期間: %s〜%s / 土日除外: %t / 休講日: %d件 / 曜日変更: %d件", m.settings.StartDate, m.settings.EndDate, m.exceptions.SkipWeekends, len(splitCSV(m.exceptions.Holidays)), len(splitCSV(m.exceptions.WeekdayChanges)))))
	if m.errorMsg != "" {
		lines = append(lines, "", badStyle.Render(m.errorMsg))
	}
	if m.statusMsg != "" {
		lines = append(lines, "", goodStyle.Render(m.statusMsg))
	}
	return m.renderBox(strings.Join(lines, "\n"))
}

func (m calendarModel) renderBox(content string) string {
	style := boxStyle
	if m.width > 0 {
		boxWidth := m.width - 2
		if boxWidth < 1 {
			boxWidth = 1
		}
		style = style.Width(boxWidth)
	}
	return style.Render(content)
}

func (m *calendarModel) exportConfiguration() error {
	path := m.exportPath
	if path == "" {
		path = "class-calendar-export-" + time.Now().Format("20060102-150405") + ".json"
	}
	courses := make([]exportCourse, 0, len(m.courses))
	for _, course := range m.courses {
		courses = append(courses, exportCourse{Term: course.Term, Title: course.Title, Weekday: course.Weekday, Period: course.Period, Teacher: course.Teacher, Location: course.Location, Note: course.Note})
	}
	document := exportDocument{
		SchemaVersion: 1, ExportedAt: time.Now().Format(time.RFC3339), CalendarID: m.calendarID, TimeZone: m.timeZone,
		StartDate: m.settings.StartDate, EndDate: m.settings.EndDate, Periods: m.settings.Periods,
		SkipWeekends: m.exceptions.SkipWeekends, Holidays: splitCSV(m.exceptions.Holidays), WeekdayChanges: splitCSV(m.exceptions.WeekdayChanges), Courses: courses,
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode export: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write export file: %w", err)
	}
	m.importPath = path
	m.statusMsg = "エクスポートしました: " + path
	return nil
}

func (m *calendarModel) importConfiguration(path string) error {
	path = expandPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read import file: %w", err)
	}
	var document exportDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode import file: %w", err)
	}
	if document.SchemaVersion != 1 {
		return fmt.Errorf("未対応の設定ファイル形式です (schema_version=%d)", document.SchemaVersion)
	}
	if document.CalendarID != "" && document.CalendarID != m.calendarID {
		return fmt.Errorf("Calendar IDが現在の登録先と異なります (現在: %s / ファイル: %s)", m.calendarID, document.CalendarID)
	}
	if document.TimeZone != "" && document.TimeZone != m.timeZone {
		return fmt.Errorf("タイムゾーンが現在の設定と異なります (現在: %s / ファイル: %s)", m.timeZone, document.TimeZone)
	}
	start, startErr := time.Parse("2006-01-02", document.StartDate)
	end, endErr := time.Parse("2006-01-02", document.EndDate)
	if startErr != nil || endErr != nil || end.Before(start) {
		return fmt.Errorf("開始日・終了日が正しくありません")
	}
	if len(document.Periods) != 5 {
		return fmt.Errorf("時限設定は5件必要です")
	}
	periods := make([]period, len(document.Periods))
	for i, value := range document.Periods {
		if _, _, err := parsePeriod(value.Start, value.End); err != nil {
			return fmt.Errorf("%d限: %w", i+1, err)
		}
		periods[i] = period{Start: strings.TrimSpace(value.Start), End: strings.TrimSpace(value.End)}
	}
	holidays := strings.Join(document.Holidays, ",")
	changes := strings.Join(document.WeekdayChanges, ",")
	if err := validateExceptions(holidays, changes); err != nil {
		return err
	}
	courses := make([]courseInput, 0, len(document.Courses))
	for _, value := range document.Courses {
		if value.Term != "全期" && value.Term != "半期" {
			return fmt.Errorf("授業 %q の期間種別が正しくありません", value.Title)
		}
		if strings.TrimSpace(value.Title) == "" {
			return fmt.Errorf("授業名が空です")
		}
		if _, err := parseWeekday(value.Weekday); err != nil {
			return fmt.Errorf("授業 %q: %w", value.Title, err)
		}
		if _, err := parsePeriodNumber(value.Period); err != nil {
			return fmt.Errorf("授業 %q: %w", value.Title, err)
		}
		courses = append(courses, courseInput{Term: value.Term, Title: value.Title, Weekday: value.Weekday, Period: value.Period, Teacher: value.Teacher, Location: value.Location, Note: value.Note})
	}
	oldSettings, oldExceptions, oldCourses := m.settings, m.exceptions, m.courses
	m.settings = calendarSettings{StartDate: document.StartDate, EndDate: document.EndDate, Periods: periods}
	m.exceptions = exceptionSettings{SkipWeekends: document.SkipWeekends, Holidays: holidays, WeekdayChanges: changes}
	m.courses = courses
	m.selected = 0
	if _, err := m.buildLessons(); err != nil {
		m.settings, m.exceptions, m.courses = oldSettings, oldExceptions, oldCourses
		return fmt.Errorf("授業設定を検証できません: %w", err)
	}
	return nil
}

func expandPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func (m calendarModel) inputsView(title string, labels []string) string {
	lines := []string{accentStyle.Render(title), mutedStyle.Render("Tab / Enter: 次へ  Shift+Tab: 戻る  最後のEnter: 保存  Esc: キャンセル"), ""}
	for i := range m.inputs {
		label := "  " + labels[i]
		if i == m.focused() {
			label = accentStyle.Render("› " + labels[i])
		}
		lines = append(lines, label+"\n    "+m.inputs[i].View())
	}
	if m.errorMsg != "" {
		lines = append(lines, "", badStyle.Render(m.errorMsg))
	}
	return m.renderBox(strings.Join(lines, "\n"))
}

func (m calendarModel) exceptionsView() string {
	lines := []string{accentStyle.Render("カレンダー例外設定"), mutedStyle.Render("Space: 土日除外の切替  Tab / Enter: 次へ・保存  Esc: キャンセル"), ""}
	weekend := "[ ]"
	if m.exceptions.SkipWeekends {
		weekend = "[x]"
	}
	lines = append(lines, fmt.Sprintf("  土日を授業なしにする %s", weekend), "")
	labels := []string{"休講日 (YYYY-MM-DD, ...)", "曜日変更 (変更先日付=変更元曜日, ...)"}
	for i := range m.inputs {
		label := "  " + labels[i]
		if i == m.focused() {
			label = accentStyle.Render("› " + labels[i])
		}
		lines = append(lines, label+"\n    "+m.inputs[i].View())
	}
	lines = append(lines, "", mutedStyle.Render("日本の祝日は自動除外されます。追加の休講日は上欄に入力してください。"))
	if m.errorMsg != "" {
		lines = append(lines, "", badStyle.Render(m.errorMsg))
	}
	return m.renderBox(strings.Join(lines, "\n"))
}

func (m calendarModel) reviewView() string {
	lessons, err := m.buildLessons()
	if err != nil {
		return badStyle.Render(err.Error())
	}
	lines := []string{accentStyle.Render("登録前の確認"), mutedStyle.Render("Enter: 一括登録を開始（並列）  b / Esc: 戻る"), ""}
	for _, lesson := range lessons {
		kind := "単発"
		if len(lesson.Recurrence) > 0 {
			kind = "毎週"
		}
		teacher := ""
		if lesson.Teacher != "" {
			teacher = " / " + lesson.Teacher
		}
		lines = append(lines, fmt.Sprintf("• %-14s%s %s  %s–%s  [%s]", lesson.Title, teacher, lesson.Start.Format("2006-01-02"), lesson.Start.Format("15:04"), lesson.End.Format("15:04"), kind))
	}
	lines = append(lines, "", fmt.Sprintf("合計 %d件（週次予定と例外による単発予定）", len(lessons)), "休講: "+strings.Join(splitCSV(m.exceptions.Holidays), ", "), "曜日変更: "+strings.Join(splitCSV(m.exceptions.WeekdayChanges), ", "))
	return m.renderBox(strings.Join(lines, "\n"))
}

func (m calendarModel) registrationView() string {
	completed, total := m.progressCompleted, m.progressTotal
	if total < 1 {
		total = 1
	}
	barWidth := 24
	if m.width > 0 && m.width < 42 {
		barWidth = m.width - 16
		if barWidth < 8 {
			barWidth = 8
		}
	}
	filled := barWidth * completed / total
	bar := strings.Repeat("█", filled) + strings.Repeat("─", barWidth-filled)
	success, failed := 0, 0
	for _, result := range m.progressResults {
		if result.Err != nil {
			failed++
		} else {
			success++
		}
	}
	lines := []string{
		accentStyle.Render("カレンダー登録中"),
		mutedStyle.Render("登録処理はバックグラウンドで実行されています。"),
		"",
		fmt.Sprintf("[%s] %d/%d 件", bar, completed, m.progressTotal),
		fmt.Sprintf("成功: %d件  失敗: %d件", success, failed),
	}
	if m.progressCurrent != "" {
		lines = append(lines, "", "処理中: "+m.progressCurrent)
	}
	return m.renderBox(strings.Join(lines, "\n"))
}

func (m calendarModel) resultsView() string {
	action := "Enter / b: メニューへ  q: 終了"
	if len(m.retryLessons) > 0 {
		action = "r: 失敗分のみ再試行  Enter / b: メニューへ  q: 終了"
	}
	lines := []string{accentStyle.Render("登録結果"), mutedStyle.Render(action), ""}
	for _, result := range m.results {
		if result.Err != nil {
			lines = append(lines, badStyle.Render("✗ "+result.Lesson.Title+": "+result.Err.Error()))
		} else {
			lines = append(lines, goodStyle.Render("✓ "+result.Lesson.Title+": "+result.Link))
		}
	}
	return m.renderBox(strings.Join(lines, "\n"))
}

func (m calendarModel) buildLessons() ([]domain.Lesson, error) {
	var lessons []domain.Lesson
	for _, course := range m.courses {
		current, err := m.courseLessons(course)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", course.Title, err)
		}
		lessons = append(lessons, current...)
	}
	return lessons, nil
}

func (m calendarModel) courseLessons(course courseInput) ([]domain.Lesson, error) {
	loc, err := time.LoadLocation(m.timeZone)
	if err != nil {
		return nil, err
	}
	startDate, err := time.ParseInLocation("2006-01-02", m.settings.StartDate, loc)
	if err != nil {
		return nil, fmt.Errorf("学期開始日: %w", err)
	}
	weekday, err := parseWeekday(course.Weekday)
	if err != nil {
		return nil, err
	}
	manualHolidays, err := m.manualHolidaySet(loc)
	if err != nil {
		return nil, err
	}
	var endDate time.Time
	term := course.Term
	if term == "" {
		term = "全期"
	}
	if term == "半期" {
		endDate, err = m.sevenLessonEnd(startDate, weekday, manualHolidays, loc)
		if err != nil {
			return nil, err
		}
	} else {
		endDate, err = time.ParseInLocation("2006-01-02", m.settings.EndDate, loc)
		if err != nil {
			return nil, fmt.Errorf("学期終了日: %w", err)
		}
	}
	if endDate.Before(startDate) {
		return nil, fmt.Errorf("学期終了日は開始日以降にしてください")
	}
	periodNumber, err := parsePeriodNumber(course.Period)
	if err != nil {
		return nil, err
	}
	firstDate := nextWeekday(startDate, weekday)
	if firstDate.After(endDate) {
		return nil, fmt.Errorf("学期内に授業曜日がありません")
	}
	p := m.settings.Periods[periodNumber-1]
	start, end, err := timesOnDate(firstDate, p.Start, p.End, loc)
	if err != nil {
		return nil, err
	}
	recurrence := []string{fmt.Sprintf("RRULE:FREQ=WEEKLY;BYDAY=%s;UNTIL=%s", weekdayCode(weekday), endDate.In(time.UTC).Format("20060102T150405Z"))}
	var moved []domain.Lesson
	for date := startDate; !date.After(endDate); date = date.AddDate(0, 0, 1) {
		if date.Weekday() != weekday {
			continue
		}
		dateKey := date.Format("2006-01-02")
		if (m.exceptions.SkipWeekends && isWeekend(date)) || manualHolidays[dateKey] || isJapaneseHoliday(date) {
			recurrence = append(recurrence, exdate(m.timeZone, date, p.Start))
		}
	}
	for _, change := range splitCSV(m.exceptions.WeekdayChanges) {
		parts := strings.SplitN(change, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("曜日変更は 変更先日付=変更元曜日 形式で入力してください")
		}
		destination, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(parts[0]), loc)
		if err != nil {
			return nil, fmt.Errorf("変更先日付: %w", err)
		}
		sourceWeekday, err := parseWeekday(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		if destination.Before(startDate) || destination.After(endDate) || sourceWeekday != weekday {
			continue
		}
		// Find the original weekday in the week containing the destination date.
		weekStart := destination.AddDate(0, 0, -((int(destination.Weekday()) + 6) % 7))
		original := weekStart.AddDate(0, 0, (int(sourceWeekday)+6)%7)
		recurrence = append(recurrence, exdate(m.timeZone, original, p.Start))
		if (m.exceptions.SkipWeekends && isWeekend(destination)) || manualHolidays[destination.Format("2006-01-02")] || isJapaneseHoliday(destination) {
			continue
		}
		movedStart, movedEnd, err := timesOnDate(destination, p.Start, p.End, loc)
		if err != nil {
			return nil, err
		}
		moved = append(moved, domain.Lesson{Title: course.Title + "（曜日変更）", Teacher: course.Teacher, Start: movedStart, End: movedEnd, Location: course.Location, Description: fmt.Sprintf("%s曜日の授業を%sへ変更\n%s", weekdayName(sourceWeekday), destination.Format("2006-01-02"), course.Note)})
	}
	return append([]domain.Lesson{{Title: course.Title, Teacher: course.Teacher, Start: start, End: end, Location: course.Location, Description: course.Note, Recurrence: recurrence}}, moved...), nil
}

func (m calendarModel) manualHolidaySet(loc *time.Location) (map[string]bool, error) {
	holidays := make(map[string]bool)
	for _, value := range splitCSV(m.exceptions.Holidays) {
		date, err := time.ParseInLocation("2006-01-02", value, loc)
		if err != nil {
			return nil, fmt.Errorf("休講日 %s: %w", value, err)
		}
		holidays[date.Format("2006-01-02")] = true
	}
	return holidays, nil
}

func (m calendarModel) sevenLessonEnd(start time.Time, weekday time.Weekday, manual map[string]bool, loc *time.Location) (time.Time, error) {
	count := 0
	for date := nextWeekday(start, weekday); ; date = date.AddDate(0, 0, 7) {
		if !m.exceptions.SkipWeekends || !isWeekend(date) {
			if !manual[date.Format("2006-01-02")] && !isJapaneseHoliday(date) {
				count++
				if count == 7 {
					return date, nil
				}
			}
		}
		if date.Year() > start.Year()+2 {
			return time.Time{}, fmt.Errorf("半期7回分の終了日を計算できません")
		}
	}
}

func exdate(zone string, date time.Time, clock string) string {
	return fmt.Sprintf("EXDATE;TZID=%s:%sT%s00", zone, date.Format("20060102"), strings.ReplaceAll(clock, ":", ""))
}
func parsePeriodNumber(value string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil || n < 1 || n > 5 {
		return 0, fmt.Errorf("時限は1〜5で指定してください")
	}
	return n, nil
}
func parsePeriod(start, end string) (time.Time, time.Time, error) {
	s, err := time.Parse("15:04", strings.TrimSpace(start))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("開始時刻: %w", err)
	}
	e, err := time.Parse("15:04", strings.TrimSpace(end))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("終了時刻: %w", err)
	}
	if !e.After(s) {
		return time.Time{}, time.Time{}, fmt.Errorf("終了時刻は開始時刻より後にしてください")
	}
	return s, e, nil
}
func validateExceptions(holidays, changes string) error {
	for _, date := range splitCSV(holidays) {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Errorf("休講日は YYYY-MM-DD 形式で入力してください: %s", date)
		}
	}
	for _, change := range splitCSV(changes) {
		parts := strings.SplitN(change, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("曜日変更は 変更先日付=変更元曜日 形式で入力してください")
		}
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(parts[0])); err != nil {
			return err
		}
		if _, err := parseWeekday(strings.TrimSpace(parts[1])); err != nil {
			return err
		}
	}
	return nil
}
func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
func periodText(settings calendarSettings, value string) string {
	n, err := parsePeriodNumber(value)
	if err != nil || len(settings.Periods) < n {
		return ""
	}
	p := settings.Periods[n-1]
	return p.Start + "–" + p.End
}
func weekdayName(value time.Weekday) string {
	return []string{"日", "月", "火", "水", "木", "金", "土"}[value]
}

func isWeekend(date time.Time) bool {
	return date.Weekday() == time.Saturday || date.Weekday() == time.Sunday
}

// isJapaneseHoliday covers the national holiday rules used for recurring
// class schedules. Explicit dates entered in the exception screen are also
// supported for school-specific holidays and substitute days.
func isJapaneseHoliday(date time.Time) bool {
	year, month, day := date.Date()
	fixed := map[time.Month][]int{
		time.January: {1}, time.February: {11, 23}, time.April: {29},
		time.May: {3, 4, 5}, time.August: {11}, time.November: {3, 23},
	}
	for _, holidayDay := range fixed[month] {
		if day == holidayDay {
			return true
		}
	}
	if day == vernalEquinoxDay(year) && month == time.March {
		return true
	}
	if day == autumnalEquinoxDay(year) && month == time.September {
		return true
	}
	switch month {
	case time.January:
		if date.Weekday() == time.Monday && day >= 8 && day <= 14 {
			return true
		} // Coming of Age
	case time.July:
		if date.Weekday() == time.Monday && day >= 15 && day <= 21 {
			return true
		} // Marine Day
	case time.September:
		if date.Weekday() == time.Monday && day >= 15 && day <= 21 {
			return true
		} // Respect for Aged
	case time.October:
		if date.Weekday() == time.Monday && day >= 8 && day <= 14 {
			return true
		} // Sports Day
	}
	// Substitute holidays: a Sunday holiday moves to the next weekday.
	if date.Weekday() != time.Sunday {
		for previous := date.AddDate(0, 0, -1); previous.Weekday() == time.Sunday || isBaseJapaneseHoliday(previous); previous = previous.AddDate(0, 0, -1) {
			if previous.Weekday() == time.Sunday && isBaseJapaneseHoliday(previous) {
				return true
			}
		}
	}
	// Citizen's holiday: a weekday between two holidays.
	if date.Weekday() != time.Sunday && isBaseJapaneseHoliday(date.AddDate(0, 0, -1)) && isBaseJapaneseHoliday(date.AddDate(0, 0, 1)) {
		return true
	}
	return false
}

func isBaseJapaneseHoliday(date time.Time) bool {
	year, month, day := date.Date()
	fixed := map[time.Month][]int{time.January: {1}, time.February: {11, 23}, time.April: {29}, time.May: {3, 4, 5}, time.August: {11}, time.November: {3, 23}}
	for _, holidayDay := range fixed[month] {
		if day == holidayDay {
			return true
		}
	}
	if month == time.March && day == vernalEquinoxDay(year) || month == time.September && day == autumnalEquinoxDay(year) {
		return true
	}
	if month == time.January && date.Weekday() == time.Monday && day >= 8 && day <= 14 {
		return true
	}
	if month == time.July && date.Weekday() == time.Monday && day >= 15 && day <= 21 {
		return true
	}
	if month == time.September && date.Weekday() == time.Monday && day >= 15 && day <= 21 {
		return true
	}
	if month == time.October && date.Weekday() == time.Monday && day >= 8 && day <= 14 {
		return true
	}
	return false
}

func vernalEquinoxDay(year int) int {
	return int(20.8431 + 0.242194*float64(year-1980) - float64((year-1980)/4))
}
func autumnalEquinoxDay(year int) int {
	return int(23.2488 + 0.242194*float64(year-1980) - float64((year-1980)/4))
}
