package main

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadLesson(t *testing.T) {
	input := "数学\n2026-10-01\n09:00\n10:30\nA-101\n前期講義\n"
	lesson, err := readLesson(bufio.NewReader(strings.NewReader(input)), "Asia/Tokyo")
	if err != nil {
		t.Fatalf("readLesson() error = %v", err)
	}
	if lesson.Title != "数学" || lesson.Location != "A-101" || lesson.Description != "前期講義" {
		t.Fatalf("unexpected lesson: %+v", lesson)
	}
	if lesson.Start.Format("2006-01-02 15:04") != "2026-10-01 09:00" {
		t.Fatalf("unexpected start: %s", lesson.Start)
	}
	if lesson.End.Sub(lesson.Start).Minutes() != 90 {
		t.Fatalf("unexpected duration: %s", lesson.End.Sub(lesson.Start))
	}
}

func TestReadLessonRejectsEndBeforeStart(t *testing.T) {
	input := "数学\n2026-10-01\n10:30\n09:00\n\n\n"
	if _, err := readLesson(bufio.NewReader(strings.NewReader(input)), "Asia/Tokyo"); err == nil {
		t.Fatal("readLesson() accepted an end time before the start time")
	}
}
