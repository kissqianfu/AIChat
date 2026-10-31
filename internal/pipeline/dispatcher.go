// Package pipeline 是有界事件队列 + 按会话串行执行的 worker 池（M6-3）。
package pipeline

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Job 是一条待处理事件。
type Job struct {
	Key string // 会话 key（scene|target|speaker），相同 key 串行
	Run func(ctx context.Context)
}

// Dispatcher 是有界队列 + 固定 worker 池；同 key 串行。
type Dispatcher struct {
	queue chan Job
	log   *slog.Logger
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	wg    sync.WaitGroup
}

// New 构造调度器；queueSize<=0 用 256，workers<=0 用 8。
func New(queueSize, workers int, logger *slog.Logger) *Dispatcher {
	if queueSize <= 0 {
		queueSize = 256
	}
	if workers <= 0 {
		workers = 8
	}
	if logger == nil {
		logger = slog.Default()
	}
	d := &Dispatcher{queue: make(chan Job, queueSize), log: logger, locks: make(map[string]*sync.Mutex)}
	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}
	return d
}

// Submit 非阻塞入队；队列满时丢弃并返回 false（不阻塞 SDK 读循环）。
func (d *Dispatcher) Submit(job Job) bool {
	select {
	case d.queue <- job:
		return true
	default:
		d.log.Warn("事件队列已满，丢弃事件", "key", job.Key)
		return false
	}
}

// Close 停止接收并等待存量任务完成。
func (d *Dispatcher) Close() {
	close(d.queue)
	d.wg.Wait()
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for job := range d.queue {
		lock := d.lockFor(job.Key)
		lock.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		func() {
			defer func() {
				if r := recover(); r != nil {
					d.log.Error("事件处理 panic", "key", job.Key, "panic", r)
				}
			}()
			job.Run(ctx)
		}()
		cancel()
		lock.Unlock()
	}
}

// lockFor 返回会话级互斥锁。
func (d *Dispatcher) lockFor(key string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	lock, ok := d.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		d.locks[key] = lock
	}
	return lock
}

// Depth returns the current queue length (metrics).
func (d *Dispatcher) Depth() int { return len(d.queue) }
