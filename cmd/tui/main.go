package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"class-calendar-adder/internal/domain"
	"class-calendar-adder/internal/infrastructure/calendar"
	"class-calendar-adder/internal/usecase"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	loadDotEnv(".env")
	credentialsPath := envOr("GOOGLE_CREDENTIALS_FILE", "credentials.json")
	calendarID := envOr("GOOGLE_CALENDAR_ID", "primary")
	timeZone := envOr("CALENDAR_TIMEZONE", "Asia/Tokyo")
	tokenPath := envOr("GOOGLE_TOKEN_FILE", ".calendar-token.json")
	settingsPath := envOr("CALENDAR_SETTINGS_FILE", ".calendar-settings.json")
	exportPath := os.Getenv("CALENDAR_EXPORT_FILE")
	importPath := os.Getenv("CALENDAR_IMPORT_FILE")

	if _, err := os.Stat(credentialsPath); err != nil {
		fmt.Printf("Google OAuth credentials fileが見つかりません: %s\n", credentialsPath)
		fmt.Println("GOOGLE_CREDENTIALS_FILE にGoogle CloudのOAuthクライアントJSONを指定してください。")
		os.Exit(1)
	}

	fmt.Println("授業カレンダー登録 TUI")
	fmt.Printf("登録先: %s (%s)\n", calendarID, timeZone)
	provider, err := calendar.NewGoogle(context.Background(), credentialsPath, calendarID, timeZone, tokenPath)
	if err != nil {
		fmt.Printf("Google Calendarの準備に失敗しました: %v\n", err)
		os.Exit(1)
	}
	adder := usecase.NewLessonAdder(provider)
	model := newCalendarModel(adder, timeZone, settingsPath, loadSettings(settingsPath))
	model.calendarID = calendarID
	model.exportPath = exportPath
	model.importPath = importPath
	program := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		// Read directly from the controlling terminal so IME input is not
		// affected when stdin is wrapped or redirected by the launcher.
		tea.WithInputTTY(),
	)
	if _, err := program.Run(); err != nil {
		fmt.Printf("TUIを起動できませんでした: %v\n", err)
	}
}

// loadDotEnv loads simple KEY=VALUE entries without overriding variables
// explicitly supplied by the shell. Secrets remain in the process environment
// and are never printed.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		_ = os.Setenv(key, value)
	}
}

func readSchedule(reader *bufio.Reader, timeZone string) ([]domain.Lesson, error) {
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return nil, fmt.Errorf("タイムゾーン %q: %w", timeZone, err)
	}
	startDateText, err := ask(reader, "学期開始日 (YYYY-MM-DD)")
	if err != nil {
		return nil, err
	}
	endDateText, err := ask(reader, "学期終了日 (YYYY-MM-DD)")
	if err != nil {
		return nil, err
	}
	startDate, err := time.ParseInLocation("2006-01-02", startDateText, loc)
	if err != nil {
		return nil, fmt.Errorf("学期開始日: %w", err)
	}
	endDate, err := time.ParseInLocation("2006-01-02", endDateText, loc)
	if err != nil {
		return nil, fmt.Errorf("学期終了日: %w", err)
	}
	if endDate.Before(startDate) {
		return nil, fmt.Errorf("学期終了日は開始日以降にしてください")
	}

	var lessons []domain.Lesson
	for {
		title, err := ask(reader, "授業名（空欄で入力完了）")
		if err != nil {
			return nil, err
		}
		if title == "" {
			break
		}
		weekdayText, err := ask(reader, "曜日（月/火/水/木/金/土/日）")
		if err != nil {
			return nil, err
		}
		weekday, err := parseWeekday(weekdayText)
		if err != nil {
			return nil, err
		}
		startClock, err := ask(reader, "開始時刻 (HH:MM)")
		if err != nil {
			return nil, err
		}
		endClock, err := ask(reader, "終了時刻 (HH:MM)")
		if err != nil {
			return nil, err
		}
		location, err := ask(reader, "教室 (任意)")
		if err != nil {
			return nil, err
		}
		description, err := ask(reader, "メモ (任意)")
		if err != nil {
			return nil, err
		}
		firstDate := nextWeekday(startDate, weekday)
		startTime, endTime, err := timesOnDate(firstDate, startClock, endClock, loc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", title, err)
		}
		recurrence := []string{fmt.Sprintf("RRULE:FREQ=WEEKLY;BYDAY=%s;UNTIL=%s", weekdayCode(weekday), endDate.In(time.UTC).Format("20060102T150405Z"))}
		excluded := []string{}
		var movedLessons []domain.Lesson
		fmt.Println("振替がある場合は、元の日付と振替先の日付を入力してください。空欄で次の授業へ進みます。")
		for {
			originalText, err := ask(reader, "振替元の日付 (YYYY-MM-DD / 空欄で終了)")
			if err != nil {
				return nil, err
			}
			if originalText == "" {
				break
			}
			movedText, err := ask(reader, "振替先の日付 (YYYY-MM-DD)")
			if err != nil {
				return nil, err
			}
			original, err := time.ParseInLocation("2006-01-02", originalText, loc)
			if err != nil {
				return nil, fmt.Errorf("振替元の日付: %w", err)
			}
			moved, err := time.ParseInLocation("2006-01-02", movedText, loc)
			if err != nil {
				return nil, fmt.Errorf("振替先の日付: %w", err)
			}
			if original.Weekday() != weekday {
				return nil, fmt.Errorf("振替元 %s は通常授業の曜日ではありません", originalText)
			}
			movedStart, movedEnd, err := timesOnDate(moved, startClock, endClock, loc)
			if err != nil {
				return nil, err
			}
			excluded = append(excluded, fmt.Sprintf("EXDATE;TZID=%s:%s", timeZone, original.Format("20060102T150405")))
			movedLessons = append(movedLessons, domain.Lesson{Title: title + "（振替）", Start: movedStart, End: movedEnd, Location: location, Description: fmt.Sprintf("振替元: %s\n%s", originalText, description)})
		}
		recurrence = append(recurrence, excluded...)
		lessons = append(lessons, domain.Lesson{Title: title, Start: startTime, End: endTime, Location: location, Description: description, Recurrence: recurrence})
		lessons = append(lessons, movedLessons...)
	}
	return lessons, nil
}

func timesOnDate(date time.Time, startClock, endClock string, loc *time.Location) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("15:04", startClock, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("開始時刻: %w", err)
	}
	end, err := time.ParseInLocation("15:04", endClock, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("終了時刻: %w", err)
	}
	start = time.Date(date.Year(), date.Month(), date.Day(), start.Hour(), start.Minute(), 0, 0, loc)
	end = time.Date(date.Year(), date.Month(), date.Day(), end.Hour(), end.Minute(), 0, 0, loc)
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("終了時刻は開始時刻より後にしてください")
	}
	return start, end, nil
}

func parseWeekday(value string) (time.Weekday, error) {
	weekdays := map[string]time.Weekday{"月": time.Monday, "火": time.Tuesday, "水": time.Wednesday, "木": time.Thursday, "金": time.Friday}
	if weekday, ok := weekdays[strings.TrimSpace(value)]; ok {
		return weekday, nil
	}
	return time.Sunday, fmt.Errorf("授業曜日は月・火・水・木・金から選択してください")
}

func weekdayCode(weekday time.Weekday) string {
	return []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[weekday]
}

func nextWeekday(date time.Time, weekday time.Weekday) time.Time {
	days := (int(weekday) - int(date.Weekday()) + 7) % 7
	return date.AddDate(0, 0, days)
}

func readLesson(reader *bufio.Reader, timeZone string) (domain.Lesson, error) {
	title, err := ask(reader, "授業名")
	if err != nil {
		return domain.Lesson{}, err
	}
	date, err := ask(reader, "日付 (YYYY-MM-DD)")
	if err != nil {
		return domain.Lesson{}, err
	}
	start, err := ask(reader, "開始時刻 (HH:MM)")
	if err != nil {
		return domain.Lesson{}, err
	}
	end, err := ask(reader, "終了時刻 (HH:MM)")
	if err != nil {
		return domain.Lesson{}, err
	}
	location, err := ask(reader, "教室 (任意)")
	if err != nil {
		return domain.Lesson{}, err
	}
	description, err := ask(reader, "メモ (任意)")
	if err != nil {
		return domain.Lesson{}, err
	}
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return domain.Lesson{}, fmt.Errorf("タイムゾーン %q: %w", timeZone, err)
	}
	startTime, err := time.ParseInLocation("2006-01-02 15:04", date+" "+start, loc)
	if err != nil {
		return domain.Lesson{}, fmt.Errorf("開始日時: %w", err)
	}
	endTime, err := time.ParseInLocation("2006-01-02 15:04", date+" "+end, loc)
	if err != nil {
		return domain.Lesson{}, fmt.Errorf("終了日時: %w", err)
	}
	if title == "" {
		return domain.Lesson{}, fmt.Errorf("授業名は必須です")
	}
	if !endTime.After(startTime) {
		return domain.Lesson{}, fmt.Errorf("終了時刻は開始時刻より後にしてください")
	}
	return domain.Lesson{Title: title, Start: startTime, End: endTime, Location: location, Description: description}, nil
}

func ask(reader *bufio.Reader, label string) (string, error) {
	fmt.Printf("%s: ", label)
	value, err := reader.ReadString('\n')
	return strings.TrimSpace(value), err
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
