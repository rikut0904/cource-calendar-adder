package domain

import "time"

// Lesson is the information required to create one calendar event.
type Lesson struct {
	Title       string
	Teacher     string
	Start       time.Time
	End         time.Time
	Description string
	Location    string
	// Recurrence contains Google Calendar recurrence rules. Empty means one event.
	Recurrence []string
}
