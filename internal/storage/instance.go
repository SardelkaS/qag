package storage

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"time"
)

const instanceLockID = int64(0x716167617070)

var ErrInstanceBusy = errors.New("другой экземпляр qag уже использует эту БД")

type instanceLock struct {
	mu   sync.Mutex
	conn *pgxpool.Conn
	stop chan struct{}
	lost chan struct{}
	done chan struct{}
	once sync.Once
}

func (s *Store) AcquireInstance(ctx context.Context) error {
	if s.instance != nil {
		return ErrInstanceBusy
	}
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return ErrUnavailable
	}
	var acquired bool
	if err = conn.QueryRow(ctx, dbscope.Render(conn, `SELECT pg_try_advisory_lock($1)`), s.Namespace.Lock(instanceLockID)).Scan(&acquired); err != nil {
		conn.Release()
		return ErrUnavailable
	}
	if !acquired {
		conn.Release()
		return ErrInstanceBusy
	}
	s.instance = &instanceLock{conn: conn, stop: make(chan struct{}), lost: make(chan struct{}), done: make(chan struct{})}
	go s.watchInstance()
	return nil
}
func (s *Store) InstanceLost() <-chan struct{} {
	if s.instance == nil {
		return nil
	}
	return s.instance.lost
}
func (s *Store) watchInstance() {
	i := s.instance
	defer close(i.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-i.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			i.mu.Lock()
			err := i.conn.Ping(ctx)
			i.mu.Unlock()
			cancel()
			if err != nil {
				close(i.lost)
				return
			}
		}
	}
}
func (s *Store) releaseInstance() {
	i := s.instance
	if i == nil {
		return
	}
	i.once.Do(func() {
		close(i.stop)
		<-i.done
		i.mu.Lock()
		defer i.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = i.conn.Exec(ctx, dbscope.Render(i.conn, `SELECT pg_advisory_unlock($1)`), s.Namespace.Lock(instanceLockID))
		i.conn.Release()
	})
}
