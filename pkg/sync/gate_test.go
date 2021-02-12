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
	"runtime"
	"testing"
	"time"
)

func TestGateBasicEnter(t *testing.T) {
	var g Gate

	if !g.Enter() {
		t.Fatalf("Failed to enter when it should be allowed")
	}

	g.Leave()

	g.Close()

	if g.Enter() {
		t.Fatalf("Allowed to enter when it should fail")
	}
}

func enterFunc(t *testing.T, g *Gate, enter, leave, reenter chan struct{}, done1, done2, done3 *WaitGroup) {
	// Wait until instructed to enter.
	<-enter
	if !g.Enter() {
		t.Errorf("Failed to enter when it should be allowed")
	}

	done1.Done()

	// Wait until instructed to leave.
	<-leave
	g.Leave()

	done2.Done()

	// Wait until instructed to reenter.
	<-reenter
	if g.Enter() {
		t.Errorf("Allowed to enter when it should fail")
	}
	done3.Done()
}

func TestGateConcurrentEnter(t *testing.T) {
	var g Gate
	var done1, done2, done3 WaitGroup

	// Create 1000 worker goroutines.
	enter := make(chan struct{})
	leave := make(chan struct{})
	reenter := make(chan struct{})
	done1.Add(1000)
	done2.Add(1000)
	done3.Add(1000)
	for i := 0; i < 1000; i++ {
		go enterFunc(t, &g, enter, leave, reenter, &done1, &done2, &done3)
	}

	// Tell them all to enter, then leave.
	close(enter)
	done1.Wait()

	close(leave)
	done2.Wait()

	// Close the gate, then have the workers try to enter again.
	g.Close()
	close(reenter)
	done3.Wait()
}

func closeFunc(g *Gate, done chan struct{}) {
	g.Close()
	close(done)
}

func TestGateCloseWaits(t *testing.T) {
	var g Gate

	// Enter 10 times.
	for i := 0; i < 10; i++ {
		if !g.Enter() {
			t.Fatalf("Failed to enter when it should be allowed")
		}
	}

	// Launch closer. Check that it doesn't complete.
	done := make(chan struct{})
	go closeFunc(&g, done)

	for i := 0; i < 10; i++ {
		select {
		case <-done:
			t.Fatalf("Close function completed too soon")
		case <-time.After(100 * time.Millisecond):
		}

		g.Leave()
	}

	// Now the closer must complete.
	<-done
}

func worker(g *Gate, done *WaitGroup) {
	for {
		if !g.Enter() {
			break
		}
		// Golang before v1.14 doesn't preempt busyloops.
		runtime.Gosched()
		g.Leave()
	}
	done.Done()
}

func TestGateConcurrentAll(t *testing.T) {
	var g Gate
	var done WaitGroup

	// Launch 1000 goroutines to concurrently enter/leave.
	done.Add(1000)
	for i := 0; i < 1000; i++ {
		go worker(&g, &done)
	}

	// Wait for the goroutines to do some work, then close the gate.
	time.Sleep(2 * time.Second)
	g.Close()

	// Wait for all of them to complete.
	done.Wait()
}

func BenchmarkGateEnterLeave(b *testing.B) {
	var g Gate
	for i := 0; i < b.N; i++ {
		g.Enter()
		g.Leave()
	}
}

func BenchmarkGateClose(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var g Gate
		g.Close()
	}
}

func BenchmarkGateEnterLeaveAsyncClose(b *testing.B) {
	var wg WaitGroup
	wg.Add(b.N)
	for i := 0; i < b.N; i++ {
		var g Gate
		g.Enter()
		go func() {
			defer wg.Done()
			g.Leave()
		}()
		g.Close()
	}
	wg.Wait()
}
