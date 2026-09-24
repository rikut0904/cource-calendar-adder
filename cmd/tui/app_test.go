package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"class-calendar-adder/internal/domain"
	"class-calendar-adder/internal/usecase"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCourseLessonsCreatesWeeklySeriesAndMove(t *testing.T) {
	m := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)})
	lessons, err := m.courseLessons(courseInput{
		Title: "数学", Weekday: "月", Period: "1", Teacher: "山田先生",
	})
	if err != nil {
		t.Fatalf("courseLessons() error = %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("got %d lessons, want one weekly series", len(lessons))
	}
	if len(lessons[0].Recurrence) != 3 || lessons[0].Start.Format("15:04") != "08:40" || lessons[0].End.Format("15:04") != "10:20" {
		t.Fatalf("unexpected recurrence: %#v", lessons[0].Recurrence)
	}
	if !strings.Contains(lessons[0].Recurrence[1], "T084000") {
		t.Fatalf("invalid EXDATE format: %#v", lessons[0].Recurrence)
	}
	if lessons[0].Teacher != "山田先生" {
		t.Fatalf("teacher = %q", lessons[0].Teacher)
	}

	m.exceptions = exceptionSettings{Holidays: "2026-04-06", WeekdayChanges: "2026-04-08=月"}
	lessons, err = m.courseLessons(courseInput{Title: "数学", Weekday: "月", Period: "1"})
	if err != nil || len(lessons) != 2 {
		t.Fatalf("got lessons=%d err=%v, want weekly series and one weekday-change event", len(lessons), err)
	}
	if len(lessons[0].Recurrence) != 5 || lessons[1].Start.Format("2006-01-02") != "2026-04-08" {
		t.Fatalf("unexpected exceptions: recurrence=%#v moved=%+v", lessons[0].Recurrence, lessons[1])
	}
}

func TestWeekdaySelectorCycles(t *testing.T) {
	m := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)})
	m.openCourseForm(-1)
	m.inputs[0].Blur()
	m.inputs[1].Focus()
	m.inputs[2].SetValue("月")
	m.changeWeekday(1)
	if got := m.inputs[2].Value(); got != "火" {
		t.Fatalf("weekday after next = %q", got)
	}
	m.changeWeekday(-1)
	if got := m.inputs[2].Value(); got != "月" {
		t.Fatalf("weekday after previous = %q", got)
	}
}

func TestJapaneseRunesReachTextInput(t *testing.T) {
	m := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)})
	m.openCourseForm(-1)
	m.inputs[0].Blur()
	m.inputs[1].Focus()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("数学")})
	if got := m.inputs[1].Value(); got != "数学" {
		t.Fatalf("Japanese input = %q", got)
	}
}

func TestSpaceTogglesWeekendExclusion(t *testing.T) {
	m := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)})
	m.openExceptionsForm()
	if !m.exceptions.SkipWeekends {
		t.Fatal("weekend exclusion should be enabled initially")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	next := updated.(calendarModel)
	if next.exceptions.SkipWeekends {
		t.Fatal("space did not disable weekend exclusion")
	}
}

func TestHalfTermEndsAfterSevenNonHolidayLessons(t *testing.T) {
	m := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)})
	lessons, err := m.courseLessons(courseInput{Term: "半期", Title: "数学", Weekday: "月", Period: "1"})
	if err != nil {
		t.Fatalf("courseLessons() error = %v", err)
	}
	if len(lessons) != 1 || lessons[0].Recurrence[0] == "" {
		t.Fatalf("unexpected half-term lessons: %+v", lessons)
	}
	if got := lessons[0].Recurrence[0]; !strings.Contains(got, "UNTIL=20260524T150000Z") {
		t.Fatalf("half-term end = %s", got)
	}
}

func TestRegistrationErrorReturnsToMenu(t *testing.T) {
	m := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)})
	m.screen = screenResults
	m.results = []usecase.AddResult{{Lesson: domain.Lesson{Title: "数学"}, Err: errors.New("登録失敗")}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(calendarModel).screen != screenCourses {
		t.Fatal("registration error did not return to menu")
	}
}

func TestExportConfigurationContainsRegistrationDataWithoutSecrets(t *testing.T) {
	path := t.TempDir() + "/schedule.json"
	m := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)})
	m.calendarID, m.exportPath = "target@example.com", path
	m.courses = []courseInput{{Term: "半期", Title: "数学", Weekday: "月", Period: "1", Teacher: "山田先生"}}
	m.exceptions.Holidays = "2026-05-01"
	if err := m.exportConfiguration(); err != nil {
		t.Fatalf("exportConfiguration() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	text := string(data)
	for _, want := range []string{"target@example.com", "2026-04-01", "数学", "山田先生", "2026-05-01"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export does not contain %q: %s", want, text)
		}
	}
	for _, secret := range []string{"credentials.json", ".calendar-token.json", "access_token"} {
		if strings.Contains(text, secret) {
			t.Fatalf("export contains secret field %q", secret)
		}
	}
}

func TestImportConfigurationRestoresRegistrationData(t *testing.T) {
	path := t.TempDir() + "/schedule.json"
	settings := calendarSettings{StartDate: "2026-04-01", EndDate: "2026-07-31", Periods: append([]period(nil), defaultPeriods...)}
	source := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", settings)
	source.calendarID, source.exportPath = "target@example.com", path
	source.courses = []courseInput{{Term: "半期", Title: "数学", Weekday: "月", Period: "1", Teacher: "山田先生", Location: "A101", Note: "持ち物"}}
	source.exceptions = exceptionSettings{SkipWeekends: true, Holidays: "2026-05-01", WeekdayChanges: "2026-04-08=月"}
	if err := source.exportConfiguration(); err != nil {
		t.Fatalf("exportConfiguration() error = %v", err)
	}

	target := newCalendarModel(usecase.LessonAdder{}, "Asia/Tokyo", ".calendar-settings-test.json", calendarSettings{Periods: append([]period(nil), defaultPeriods...)})
	target.calendarID = "target@example.com"
	if err := target.importConfiguration(path); err != nil {
		t.Fatalf("importConfiguration() error = %v", err)
	}
	if target.settings.StartDate != "2026-04-01" || target.settings.EndDate != "2026-07-31" {
		t.Fatalf("imported date range = %s..%s", target.settings.StartDate, target.settings.EndDate)
	}
	if len(target.courses) != 1 || target.courses[0].Teacher != "山田先生" || target.courses[0].Location != "A101" {
		t.Fatalf("imported courses = %+v", target.courses)
	}
	if target.exceptions.Holidays != "2026-05-01" || target.exceptions.WeekdayChanges != "2026-04-08=月" {
		t.Fatalf("imported exceptions = %+v", target.exceptions)
	}
}
