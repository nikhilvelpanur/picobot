package cron

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Job represents a scheduled task.
type Job struct {
	ID        string
	Name      string
	Message   string
	FireAt    time.Time
	Channel   string // originating channel (e.g., "telegram")
	ChatID    string // originating chat ID
	Recurring bool   // if true, re-schedule after firing
	Interval  time.Duration
	fired     bool
}

// FireCallback is called when a job fires. The scheduler passes the job details.
type FireCallback func(job Job)

// Scheduler manages in-memory scheduled jobs and fires them when due.
type Scheduler struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	callback FireCallback
	nextID   int
	running  bool
}

// NewScheduler creates a new scheduler with the given fire callback.
func NewScheduler(callback FireCallback) *Scheduler {
	return &Scheduler{
		jobs:     make(map[string]*Job),
		callback: callback,
	}
}

// Add schedules a new job. Returns the job ID.
// If a job with the same name already exists, it is replaced (dedup).
func (s *Scheduler) Add(name, message string, delay time.Duration, channel, chatID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Dedup: remove any existing job with the same name
	for id, j := range s.jobs {
		if j.Name == name {
			log.Printf("cron: replacing existing job %q (%s) with new schedule", name, id)
			delete(s.jobs, id)
			break
		}
	}
	s.nextID++
	id := fmt.Sprintf("job-%d", s.nextID)
	s.jobs[id] = &Job{
		ID:      id,
		Name:    name,
		Message: message,
		FireAt:  time.Now().Add(delay),
		Channel: channel,
		ChatID:  chatID,
	}
	log.Printf("cron: scheduled job %q (%s) to fire in %v", name, id, delay)
	return id
}

// AddRecurring schedules a recurring job. Returns the job ID.
// If a recurring job with the same name already exists, it is replaced (dedup).
func (s *Scheduler) AddRecurring(name, message string, interval time.Duration, channel, chatID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Dedup: remove any existing job with the same name
	for id, j := range s.jobs {
		if j.Name == name {
			log.Printf("cron: replacing existing recurring job %q (%s) with new schedule", name, id)
			delete(s.jobs, id)
			break
		}
	}
	s.nextID++
	id := fmt.Sprintf("job-%d", s.nextID)
	s.jobs[id] = &Job{
		ID:        id,
		Name:      name,
		Message:   message,
		FireAt:    time.Now().Add(interval),
		Channel:   channel,
		ChatID:    chatID,
		Recurring: true,
		Interval:  interval,
	}
	log.Printf("cron: scheduled recurring job %q (%s) every %v", name, id, interval)
	return id
}

// Cancel removes a job by ID. Returns true if found.
func (s *Scheduler) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; ok {
		delete(s.jobs, id)
		log.Printf("cron: cancelled job %s", id)
		return true
	}
	return false
}

// CancelByName removes a job by name. Returns true if found.
func (s *Scheduler) CancelByName(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, j := range s.jobs {
		if j.Name == name {
			delete(s.jobs, id)
			log.Printf("cron: cancelled job %q (%s)", name, id)
			return true
		}
	}
	return false
}

// List returns all pending jobs.
func (s *Scheduler) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		result = append(result, *j)
	}
	return result
}

// Start begins the scheduler tick loop. Call in a goroutine.
func (s *Scheduler) Start(done <-chan struct{}) {
	s.running = true
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	log.Println("cron: scheduler started")
	for {
		select {
		case <-done:
			s.running = false
			log.Println("cron: scheduler stopped")
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

// tick checks all jobs and fires any that are due.
func (s *Scheduler) tick(now time.Time) {
	s.mu.Lock()
	// collect jobs to fire
	var toFire []*Job
	for _, j := range s.jobs {
		if !j.fired && now.After(j.FireAt) {
			toFire = append(toFire, j)
		}
	}
	// handle fired jobs while still holding lock
	for _, j := range toFire {
		if j.Recurring {
			j.FireAt = now.Add(j.Interval)
		} else {
			j.fired = true
			delete(s.jobs, j.ID)
		}
	}
	s.mu.Unlock()

	// fire callbacks outside lock
	for _, j := range toFire {
		log.Printf("cron: firing job %q (%s): %s", j.Name, j.ID, j.Message)
		if s.callback != nil {
			s.callback(*j)
		}
	}
}
