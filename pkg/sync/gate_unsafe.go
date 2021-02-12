// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sync

import (
	"math"
	"sync/atomic"
	"unsafe"
)

// Gate is a synchronization primitive that allows concurrent goroutines to
// "enter" it as long as it hasn't been closed yet. Once it's been closed,
// goroutines cannot enter it anymore, but are allowed to leave, and the closer
// will be informed when all goroutines have left.
//
// Gate is similar to WaitGroup:
//
// - Gate.Enter() is analogous to WaitGroup.Add(1), but may be called even if
// the Gate counter is 0 and fails if Gate.Close() has been called.
//
// - Gate.Leave() is equivalent to WaitGroup.Done().
//
// - Gate.Close() is analogous to WaitGroup.Wait(), but also causes future
// calls to Gate.Enter() to fail and may only be called once, from a single
// goroutine.
//
// This is useful, for example, in cases when a goroutine is trying to clean up
// an object for which multiple goroutines have pointers. In such a case, users
// would be required to enter and leave the Gate, and the cleaner would wait
// until all users are gone (and no new ones are allowed) before proceeding.
//
// Users:
//
//	if !g.Enter() {
//		// Gate is closed, we can't use the object.
//		return
//	}
//
//	// Do something with object.
//	[...]
//
//	g.Leave()
//
// Closer:
//
//	// Prevent new users from using the object, and wait for the existing
//	// ones to complete.
//	g.Close()
//
//	// Clean up the object.
//	[...]
//
type Gate struct {
	userCount int32
	closingG  uintptr
}

const preparingG = 1

// Enter tries to enter the gate. It will succeed if it hasn't been closed yet,
// in which case the caller must eventually call Leave().
//
// This function is thread-safe.
func (g *Gate) Enter() bool {
	if atomic.AddInt32(&g.userCount, 1) > 0 {
		return true
	}
	g.enterFailed()
	return false
}

// As of this writing, inlining this into Enter prevents Enter from being
// inlined.
//go:noinline
func (g *Gate) enterFailed() {
	atomic.AddInt32(&g.userCount, -1)
	g.leaveClosed()
}

// Leave leaves the gate. This must only be called after a successful call to
// Enter(). If the gate has been closed and this is the last one inside the
// gate, it will notify the closer that the gate is done.
//
// This function is thread-safe.
func (g *Gate) Leave() {
	if atomic.AddInt32(&g.userCount, -1) < 0 {
		g.leaveClosed()
	}
}

//go:noinline
func (g *Gate) leaveClosed() {
	if atomic.LoadUintptr(&g.closingG) == 0 {
		return
	}
	if g := atomic.SwapUintptr(&g.closingG, 0); g > preparingG {
		goready(g, 0)
	}
}

// Close closes the gate for entering, and waits until all goroutines [that are
// currently inside the gate] leave before returning.
//
// Only one goroutine can call this function.
func (g *Gate) Close() {
	if v := atomic.AddInt32(&g.userCount, math.MinInt32); v == math.MinInt32 {
		// g.userCount was already 0.
		return
	} else if v >= 0 {
		panic("Close of closed sync.Gate")
	}
	for {
		atomic.StoreUintptr(&g.closingG, preparingG)
		if atomic.LoadInt32(&g.userCount) == math.MinInt32 {
			atomic.StoreUintptr(&g.closingG, 0)
			return
		}
		// WaitReasonChanReceive/TraceEvGoBlockRecv are arbitrarily chosen to
		// be consistent with the use of a channel in previous versions of
		// Gate.
		gopark(gateCommit, unsafe.Pointer(&g.closingG), WaitReasonChanReceive, TraceEvGoBlockRecv, 0)
		// TODO: Can gopark return spuriously? If not, one gopark() is enough;
		// we don't need to loop.
	}
}

//go:norace
//go:nosplit
func gateCommit(g uintptr, closingG unsafe.Pointer) bool {
	return RaceUncheckedAtomicCompareAndSwapUintptr((*uintptr)(closingG), preparingG, g)
}
