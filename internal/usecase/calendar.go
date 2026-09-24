package usecase

import (
	"class-calendar-adder/internal/domain"
	"sync"
)

// Calendar is the application boundary for an external calendar provider.
type Calendar interface {
	CreateLesson(lesson domain.Lesson) (string, error)
}

type LessonAdder struct {
	calendar Calendar
}

func NewLessonAdder(calendar Calendar) LessonAdder {
	return LessonAdder{calendar: calendar}
}

func (a LessonAdder) Add(lesson domain.Lesson) (string, error) {
	return a.calendar.CreateLesson(lesson)
}

type AddResult struct {
	Lesson domain.Lesson
	Link   string
	Err    error
}

// AddAll starts all calendar requests after input is complete. A small worker
// pool keeps the goroutine-based registration from bursting the provider API.
func (a LessonAdder) AddAll(lessons []domain.Lesson) []AddResult {
	return a.AddAllProgress(lessons, nil)
}

// AddAllProgress registers lessons with the same bounded worker pool as
// AddAll and calls progress after each lesson completes. The callback is
// invoked from a worker goroutine and should return quickly.
func (a LessonAdder) AddAllProgress(lessons []domain.Lesson, progress func(completed, total int, result AddResult)) []AddResult {
	results := make([]AddResult, len(lessons))
	if len(lessons) == 0 {
		return results
	}
	workerCount := 3
	if len(lessons) < workerCount {
		workerCount = len(lessons)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	completed := 0
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lesson := lessons[i]
				link, err := a.Add(lesson)
				result := AddResult{Lesson: lesson, Link: link, Err: err}
				results[i] = result
				if progress != nil {
					progressMu.Lock()
					completed++
					current := completed
					progressMu.Unlock()
					progress(current, len(lessons), result)
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range lessons {
			jobs <- i
		}
	}()
	wg.Wait()
	return results
}
