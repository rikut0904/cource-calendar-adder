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
	var progressMu sync.Mutex
	completed := 0

	// Recurring events are completed first. This is important for weekday
	// changes: their original instances must be cancelled before the moved
	// single events are created.
	var recurring, singles []int
	for i, lesson := range lessons {
		if len(lesson.Recurrence) > 0 {
			recurring = append(recurring, i)
		} else {
			singles = append(singles, i)
		}
	}
	registerBatch := func(indices []int) {
		if len(indices) == 0 {
			return
		}
		workerCount := 3
		if len(indices) < workerCount {
			workerCount = len(indices)
		}
		jobs := make(chan int)
		var wg sync.WaitGroup
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
			for _, i := range indices {
				jobs <- i
			}
		}()
		wg.Wait()
	}
	registerBatch(recurring)
	registerBatch(singles)
	return results
}
