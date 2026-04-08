package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Bucket struct {
	Name      string    `json:"name"`
	Priority  int       `json:"priority"`
	Tasks     []*Task   `json:"tasks"`
	MaxSize   int       `json:"max_size"`
	Processed int       `json:"processed"`
	Failed    int       `json:"failed"`
	CreatedAt time.Time `json:"created_at"`
}

type BucketQueue struct {
	buckets  map[string]*Bucket
	tasks    map[string]*Task
	mu       sync.RWMutex
	maxTasks int
	sched    *BucketScheduler
	autoFix  *AutoFix
	pico     *Picoclaw
}

type BucketScheduler struct {
	queue *BucketQueue
	stop  chan bool
}

const (
	BucketCritical = "critical"
	BucketHigh     = "high"
	BucketMedium   = "medium"
	BucketLow      = "low"
	BucketBacklog  = "backlog"
)

func NewBucketQueue(pico *Picoclaw) *BucketQueue {
	bq := &BucketQueue{
		buckets:  make(map[string]*Bucket),
		tasks:    make(map[string]*Task),
		maxTasks: 1000,
		pico:     pico,
	}
	bq.sched = &BucketScheduler{queue: bq}
	bq.initBuckets()
	return bq
}

func (bq *BucketQueue) initBuckets() {
	bq.buckets[BucketCritical] = &Bucket{
		Name:      BucketCritical,
		Priority:  5,
		MaxSize:   10,
		Tasks:     []*Task{},
		CreatedAt: time.Now(),
	}
	bq.buckets[BucketHigh] = &Bucket{
		Name:      BucketHigh,
		Priority:  4,
		MaxSize:   50,
		Tasks:     []*Task{},
		CreatedAt: time.Now(),
	}
	bq.buckets[BucketMedium] = &Bucket{
		Name:      BucketMedium,
		Priority:  3,
		MaxSize:   100,
		Tasks:     []*Task{},
		CreatedAt: time.Now(),
	}
	bq.buckets[BucketLow] = &Bucket{
		Name:      BucketLow,
		Priority:  2,
		MaxSize:   200,
		Tasks:     []*Task{},
		CreatedAt: time.Now(),
	}
	bq.buckets[BucketBacklog] = &Bucket{
		Name:      BucketBacklog,
		Priority:  1,
		MaxSize:   1000,
		Tasks:     []*Task{},
		CreatedAt: time.Now(),
	}
}

func (bq *BucketQueue) AddTask(title, desc string, priority int) *Task {
	bq.mu.Lock()
	defer bq.mu.Unlock()

	bucketName := bq.priorityToBucket(priority)
	bucket := bq.buckets[bucketName]

	task := &Task{
		ID:          fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Title:       title,
		Description: desc,
		Status:      "pending",
		Priority:    priority,
		CreatedAt:   time.Now(),
	}

	if len(bucket.Tasks) < bucket.MaxSize {
		bucket.Tasks = append(bucket.Tasks, task)
	} else {
		bq.buckets[BucketBacklog].Tasks = append(bq.buckets[BucketBacklog].Tasks, task)
	}

	bq.tasks[task.ID] = task
	bq.saveState()
	return task
}

func (bq *BucketQueue) priorityToBucket(priority int) string {
	switch {
	case priority >= 5:
		return BucketCritical
	case priority >= 4:
		return BucketHigh
	case priority >= 3:
		return BucketMedium
	case priority >= 2:
		return BucketLow
	default:
		return BucketBacklog
	}
}

func (bq *BucketQueue) GetNextTask() *Task {
	bq.mu.Lock()
	defer bq.mu.Unlock()

	buckets := []*Bucket{
		bq.buckets[BucketCritical],
		bq.buckets[BucketHigh],
		bq.buckets[BucketMedium],
		bq.buckets[BucketLow],
		bq.buckets[BucketBacklog],
	}

	for _, bucket := range buckets {
		if len(bucket.Tasks) > 0 {
			task := bucket.Tasks[0]
			bucket.Tasks = bucket.Tasks[1:]
			return task
		}
	}
	return nil
}

func (bq *BucketQueue) ProcessTasks(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			task := bq.GetNextTask()
			if task == nil {
				time.Sleep(time.Second)
				continue
			}

			bq.executeTask(ctx, task, workerID)
		}
	}
}

func (bq *BucketQueue) executeTask(ctx context.Context, task *Task, workerID int) {
	task.Status = "running"

	llm := NewLLMClient("nvidia", "minimaxai/minimax-m2.5", os.Getenv("NVIDIA_API_KEY"))
	if llm.apiKey == "" {
		llm.apiKey = "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"
	}

	prompt := fmt.Sprintf("Complete this task:\n%s\n\nDescription: %s", task.Title, task.Description)
	resp, err := llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)

	bq.mu.Lock()
	defer bq.mu.Unlock()

	if err != nil {
		task.Status = "failed"
		task.Error = err.Error()
		bucket := bq.buckets[bq.priorityToBucket(task.Priority)]
		bucket.Failed++
	} else {
		task.Status = "completed"
		task.Result = resp
		task.CompletedAt = new(time.Time)
		*task.CompletedAt = time.Now()
		bucket := bq.buckets[bq.priorityToBucket(task.Priority)]
		bucket.Processed++
	}

	bq.saveState()
}

func (bq *BucketQueue) GetBucketStats() string {
	bq.mu.RLock()
	defer bq.mu.RUnlock()

	var stats strings.Builder
	stats.WriteString("📊 Bucket Queue Status\n\n")

	buckets := make([]*Bucket, 0, len(bq.buckets))
	for _, b := range bq.buckets {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Priority > buckets[j].Priority
	})

	for _, bucket := range buckets {
		bar := bq.progressBar(len(bucket.Tasks), bucket.MaxSize)
		stats.WriteString(fmt.Sprintf("%s [%s] P%d: %d/%d tasks | ✅%d ❌%d\n",
			bucket.Name, bar, bucket.Priority, len(bucket.Tasks), bucket.MaxSize, bucket.Processed, bucket.Failed))
	}

	stats.WriteString(fmt.Sprintf("\nTotal tasks in queue: %d", len(bq.tasks)))
	return stats.String()
}

func (bq *BucketQueue) progressBar(current, max int) string {
	const width = 10
	percent := float64(current) / float64(max)
	filled := int(percent * float64(width))
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func (bq *BucketQueue) saveState() {
	data, _ := json.MarshalIndent(map[string]interface{}{
		"buckets": bq.buckets,
		"tasks":   bq.tasks,
	}, "", "  ")
	os.WriteFile(filepath.Join("/root/.picoclaw/autonomous", "bucket_queue.json"), data, 0644)
}

func (bq *BucketQueue) StartWorkers(ctx context.Context, count int) {
	for i := 0; i < count; i++ {
		go bq.ProcessTasks(ctx, i)
	}
	fmt.Printf("✓ Started %d bucket workers\n", count)
}

func (bq *BucketQueue) Rebalance() {
	bq.mu.Lock()
	defer bq.mu.Unlock()

	backlog := bq.buckets[BucketBacklog]
	if len(backlog.Tasks) > 100 {
		overflow := backlog.Tasks[100:]
		backlog.Tasks = backlog.Tasks[:100]

		for _, task := range overflow {
			newBucket := bq.priorityToBucket(task.Priority)
			if len(bq.buckets[newBucket].Tasks) < bq.buckets[newBucket].MaxSize {
				bq.buckets[newBucket].Tasks = append(bq.buckets[newBucket].Tasks, task)
			}
		}
	}
}
