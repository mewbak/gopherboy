package main

const (
	// tClockRate is the rate at which the tClock increments. Completely not
	// coincidentally, this is also the Game Boy's clock speed.
	tClockRate = 4194304
	// mClockRate is the rate at which the mClock increments.
	mClockRate = tClockRate / 4
	// dividerClockRate is the rate at which the divider increments.
	dividerClockRate = mClockRate / 64
)

// timers keeps track of all timers in the Gameboy, including the TIMA.
type timers struct {
	// A clock that increments every clock cycle
	tClock int
	// A clock that increments every 4 clock cycles
	mClock int
	// A clock that increments every 64 clock cycles. Available as a register
	// in memory.
	divider uint8
	// A configurable timer, also known as the "counter" but technically
	// referred to as the TIMA. Available as a register in memory.
	tima uint8

	env *environment
}

func newTimers(env *environment) *timers {
	return &timers{env: env}
}

// tick increments the timers given the amount of cycles that have passed since
// the last call to tick. Flags interrupts as needed.
func (t *timers) tick(amount int) {
	tac := t.env.mmu.at(tacAddr)
	timaRate, timaRunning := parseTAC(tac)

	// Increment the clock
	for i := 0; i < amount; i += 4 {
		t.mClock++
		if t.mClock >= mClockRate {
			t.mClock = 0
		}
		if t.mClock%64 == 0 {
			t.divider++
		}
		// Finds how many m-clock increments should happen before a
		// TIMA increment should happen.
		clocksPerTimer := mClockRate / timaRate
		if timaRunning && t.mClock%clocksPerTimer == 0 {
			t.tima++

			timaInterruptEnabled := t.env.mmu.at(ieAddr)&0x04 == 0x04
			if t.env.interruptsEnabled && timaInterruptEnabled && t.tima == 0 {
				// Flag a TIMA overflow interrupt
				t.env.mmu.set(ifAddr, t.env.mmu.at(ifAddr)|0x04)
				// Start back up at the specified modulo value
				t.tima = t.env.mmu.at(tmaAddr)
			}
		}

		// Update the timers in memory
		t.env.mmu.set(dividerAddr, t.divider)
		t.env.mmu.set(timaAddr, t.tima)
	}

}

// parseTAC takes in a control byte and returns the configuration it supplies.
// The rate refers to the rate at which the TIMA should run. The running value
// refers to whether or not the TIMA should run in the first place.
func parseTAC(tac uint8) (rate int, running bool) {
	speedBits := tac & 0x3
	switch speedBits {
	case 0x0:
		rate = 4096
	case 0x1:
		rate = 262144
	case 0x2:
		rate = 65536
	case 0x3:
		rate = 16384
	}

	runningBit := tac & 0x4
	return rate, runningBit == 0x4
}
