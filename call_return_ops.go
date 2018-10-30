package main

import "fmt"

// call loads a 16-bit address, pushes the address of the next instruction onto
// the stack, and jumps to the loaded address.
func call(env *environment) int {
	address := combine(env.incrementPC(), env.incrementPC())
	env.pushToStack16(env.regs[regPC].get())

	env.regs[regPC].set(address)

	if printInstructions {
		fmt.Printf("CALL %#x\n", address)
	}
	return 24
}

// call loads a 16-bit address, pushes the address of the next instruction onto
// the stack, and jumps to the loaded address if the given flag is at the
// expected setting.
func callIfFlag(env *environment, flagMask uint16, isSet bool) int {
	flagState := env.regs[regF].get()&flagMask == flagMask
	address := combine(env.incrementPC(), env.incrementPC())

	conditional := getConditionalStr(flagMask, isSet)
	if printInstructions {
		fmt.Printf("CALL %v,%#x\n", conditional, address)
	}
	if flagState == isSet {
		env.pushToStack16(env.regs[regPC].get())
		env.regs[regPC].set(address)
		return 24
	} else {
		return 12
	}
}

// ret pops a 16-bit address from the stack and jumps to it.
func ret(env *environment) int {
	addr := env.popFromStack16()
	env.regs[regPC].set(addr)

	if printInstructions {
		fmt.Printf("RET\n", addr)
	}
	return 16
}

// retIfFlag pops a 16-bit address from the stack and jumps to it, but only if
// the given flag is at the expected value.
func retIfFlag(env *environment, flagMask uint16, isSet bool) int {
	flagState := env.regs[regF].get()&flagMask == flagMask

	var opClocks int
	if flagState == isSet {
		addr := env.popFromStack16()
		env.regs[regPC].set(addr)
		opClocks = 20
	} else {
		opClocks = 8
	}

	conditional := getConditionalStr(flagMask, isSet)
	if printInstructions {
		fmt.Printf("RET %v\n", conditional)
	}
	return opClocks
}

// reti pops a 16-bit address from the stack and jumps to it, then enables
// interrupts.
func reti(env *environment) int {
	addr := env.popFromStack16()
	env.regs[regPC].set(addr)

	env.interruptsEnabled = true

	if printInstructions {
		fmt.Printf("RETI\n")
	}
	return 16
}

// rst pushes the current program counter to the stack and jumps to the given
// address.
func rst(env *environment, address uint16) int {
	env.pushToStack16(env.regs[regPC].get())

	env.regs[regPC].set(address)

	if printInstructions {
		fmt.Printf("RST %#x\n", address)
	}
	return 16
}
