package workers

import (
	"context"
	"fmt"
	"sync"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// WorkerPool manages a pool of workers for job execution
type WorkerPool struct {
	ctx           context.Context
	cancel        context.CancelFunc
	numWorkers    int
	workerChan    chan func()
	wg            sync.WaitGroup
	logger        *utils.LogsManager
	cm            *utils.ConfigManager
}

// NewWorkerPool creates a new worker pool
func NewWorkerPool(ctx context.Context, numWorkers int, cm *utils.ConfigManager) *WorkerPool {
	poolCtx, cancel := context.WithCancel(ctx)

	return &WorkerPool{
		ctx:        poolCtx,
		cancel:     cancel,
		numWorkers: numWorkers,
		workerChan: make(chan func(), numWorkers),
		logger:     utils.NewLogsManager(cm),
		cm:         cm,
	}
}

// Start initializes and starts all workers in the pool
func (wp *WorkerPool) Start() {
	wp.logger.Info(fmt.Sprintf("Starting worker pool with %d workers", wp.numWorkers), "workers")

	for i := 0; i < wp.numWorkers; i++ {
		wp.wg.Add(1)
		workerID := i

		go func(id int) {
			defer wp.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					wp.logger.Error(fmt.Sprintf("Worker %d panic recovered: %v", id, r), "workers")
				}
			}()

			wp.logger.Info(fmt.Sprintf("Worker %d started", id), "workers")

			for {
				select {
				case task := <-wp.workerChan:
					// Execute the task
					wp.logger.Info(fmt.Sprintf("Worker %d executing task", id), "workers")
					task()

				case <-wp.ctx.Done():
					wp.logger.Info(fmt.Sprintf("Worker %d stopping (context done)", id), "workers")
					return
				}
			}
		}(workerID)
	}
}

// Submit submits a task to the worker pool
func (wp *WorkerPool) Submit(task func()) error {
	select {
	case wp.workerChan <- task:
		return nil
	case <-wp.ctx.Done():
		return fmt.Errorf("worker pool is shutting down")
	}
}

// Stop gracefully stops the worker pool
func (wp *WorkerPool) Stop() {
	wp.logger.Info("Stopping worker pool", "workers")
	wp.cancel()

	// Wait for all workers to finish
	wp.wg.Wait()

	// Close the worker channel
	close(wp.workerChan)

	wp.logger.Info("Worker pool stopped", "workers")
	wp.logger.Close()
}

// GetActiveWorkers returns the number of active workers
func (wp *WorkerPool) GetActiveWorkers() int {
	return wp.numWorkers
}
