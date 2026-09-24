package usecase

import (
	"class-calendar-adder/internal/domain"
	"sync"
	"testing"
	"time"
)

type concurrentCalendar struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (c *concurrentCalendar) CreateLesson(lesson domain.Lesson) (string, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return lesson.Title, nil
}

func TestLessonAdderAddAllRunsConcurrently(t *testing.T) {
	calendar := &concurrentCalendar{}
	lessons := []domain.Lesson{{Title: "A"}, {Title: "B"}, {Title: "C"}}
	results := NewLessonAdder(calendar).AddAll(lessons)
	if len(results) != len(lessons) || calendar.maxActive < 2 {
		t.Fatalf("AddAll did not run concurrently: results=%d maxActive=%d", len(results), calendar.maxActive)
	}
}
