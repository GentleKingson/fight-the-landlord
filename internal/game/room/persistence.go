package room

import (
	"context"
	"log"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/server/storage"
)

const shutdownDeleteTimeout = 5 * time.Second

type roomPersistenceQueue struct {
	pendingSave        func()
	pendingDelete      func()
	pendingAfterDelete func()
}

func (rm *RoomManager) persistenceReady() bool {
	rm.persistenceMu.Lock()
	closed := rm.persistenceClosed
	rm.persistenceMu.Unlock()
	if closed {
		return false
	}
	return rm.saveRoomFunc != nil || rm.deleteRoomFunc != nil || (rm.redisStore != nil && rm.redisStore.IsReady())
}

func (rm *RoomManager) persistenceContext() context.Context {
	if rm.ctx != nil {
		return rm.ctx
	}
	return context.Background()
}

func (rm *RoomManager) saveOperation() func(context.Context, string, *storage.RoomData) error {
	if rm.saveRoomFunc != nil {
		return rm.saveRoomFunc
	}
	if rm.redisStore == nil || !rm.redisStore.IsReady() {
		return nil
	}
	return rm.redisStore.SaveRoom
}

func (rm *RoomManager) deleteOperation() func(context.Context, string) error {
	if rm.deleteRoomFunc != nil {
		return rm.deleteRoomFunc
	}
	if rm.redisStore == nil || !rm.redisStore.IsReady() {
		return nil
	}
	return rm.redisStore.DeleteRoom
}

// PersistRoom queues an exact-identity snapshot. Taking RoomManager ownership
// while snapshotting and enqueueing gives Save and removal Delete one total
// per-code order without performing Redis I/O under manager or room locks.
func (rm *RoomManager) PersistRoom(gameRoom *Room) {
	if gameRoom == nil || !rm.persistenceReady() {
		return
	}

	rm.mu.RLock()
	if rm.rooms[gameRoom.Code] != gameRoom {
		rm.mu.RUnlock()
		return
	}
	data := gameRoom.ToRoomData()
	operation := rm.saveOperation()
	if operation != nil {
		rm.enqueuePersistenceSave(gameRoom.Code, func() {
			if err := operation(rm.persistenceContext(), gameRoom.Code, data); err != nil {
				log.Printf("保存房间 %s 到 Redis 失败: %v", gameRoom.Code, err)
			}
		})
	}
	rm.mu.RUnlock()
}

func (rm *RoomManager) saveRoomAsync(gameRoom *Room) {
	rm.PersistRoom(gameRoom)
}

// enqueueRoomDelete is called at the published-removal linearization point
// while RoomManager.mu is held, so no later Save can be ordered before it.
func (rm *RoomManager) enqueueRoomDelete(code string, identity *Room) {
	operation := rm.deleteOperation()
	rm.enqueuePersistenceDelete(code, func() {
		if operation != nil {
			// Saves obey lifecycle cancellation immediately. Deletes are
			// different: a room has already been unpublished, so it gets one
			// bounded cleanup attempt even when shutdown races with Redis I/O.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(rm.persistenceContext()), shutdownDeleteTimeout)
			defer cancel()
			if err := operation(ctx, code); err != nil {
				log.Printf("从 Redis 删除房间 %s 失败: %v", code, err)
			}
		}
		rm.finishRoomRetirement(code, identity)
	})
}

func (rm *RoomManager) finishRoomRetirement(code string, identity *Room) {
	rm.mu.Lock()
	if rm.retiringRooms[code] == identity {
		delete(rm.retiringRooms, code)
	}
	rm.mu.Unlock()
}

func (rm *RoomManager) enqueuePersistenceSave(code string, operation func()) {
	rm.persistenceMu.Lock()
	if rm.persistenceClosed {
		rm.persistenceMu.Unlock()
		return
	}
	queue, exists := rm.persistenceQueueLocked(code)
	if queue.pendingDelete != nil {
		queue.pendingAfterDelete = operation
	} else {
		queue.pendingSave = operation
	}
	if !exists {
		rm.persistenceWG.Add(1)
	}
	rm.persistenceMu.Unlock()
	if !exists {
		go func() {
			defer rm.persistenceWG.Done()
			rm.runPersistenceQueue(code, queue)
		}()
	}
}

func (rm *RoomManager) enqueuePersistenceDelete(code string, operation func()) {
	rm.persistenceMu.Lock()
	if rm.persistenceClosed {
		rm.persistenceMu.Unlock()
		return
	}
	queue, exists := rm.persistenceQueueLocked(code)
	// A Delete makes all queued pre-delete snapshots obsolete. A Save that is
	// already performing I/O is allowed to finish; the single worker executes
	// this Delete immediately afterward.
	queue.pendingSave = nil
	queue.pendingDelete = operation
	if !exists {
		rm.persistenceWG.Add(1)
	}
	rm.persistenceMu.Unlock()
	if !exists {
		go func() {
			defer rm.persistenceWG.Done()
			rm.runPersistenceQueue(code, queue)
		}()
	}
}

func (rm *RoomManager) persistenceQueueLocked(code string) (*roomPersistenceQueue, bool) {
	if rm.persistenceQueues == nil {
		rm.persistenceQueues = make(map[string]*roomPersistenceQueue)
	}
	queue, exists := rm.persistenceQueues[code]
	if !exists {
		queue = &roomPersistenceQueue{}
		rm.persistenceQueues[code] = queue
	}
	return queue, exists
}

func (rm *RoomManager) runPersistenceQueue(code string, queue *roomPersistenceQueue) {
	for {
		rm.persistenceMu.Lock()
		operation := queue.pendingDelete
		if operation != nil {
			queue.pendingDelete = nil
			queue.pendingSave = queue.pendingAfterDelete
			queue.pendingAfterDelete = nil
		} else {
			operation = queue.pendingSave
			queue.pendingSave = nil
		}
		if operation == nil {
			if rm.persistenceQueues[code] == queue {
				delete(rm.persistenceQueues, code)
			}
			rm.persistenceMu.Unlock()
			return
		}
		rm.persistenceMu.Unlock()

		operation()
	}
}
