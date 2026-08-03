package executor

/*
Note: semaphores over box ids.
*/

// BoxManager manages a pool of available isolate box IDs
type BoxManager struct {
	pool chan int
}

// NewBoxManager initializes a pool with a fixed capacity of boxes (0 to maxBoxes-1)
func NewBoxManager(maxBoxes int) *BoxManager {
	ch := make(chan int, maxBoxes)
	for i := 0; i < maxBoxes; i++ {
		ch <- i
	}
	return &BoxManager{
		pool: ch,
	}
}

// Acquire pops an available box ID from the pool (blocks if all are busy)
func (m *BoxManager) Acquire() int {
	return <-m.pool
}

// Release returns a box ID back to the pool for reuse
func (m *BoxManager) Release(boxID int) {
	m.pool <- boxID
}