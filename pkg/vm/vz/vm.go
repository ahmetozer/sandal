//go:build darwin

package vz

/*
#include <stdlib.h>
#include "vz.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"unsafe"
)

type VMState int

const (
	VMStateStopped  VMState = 0
	VMStateRunning  VMState = 1
	VMStatePaused   VMState = 2
	VMStateError    VMState = 3
	VMStateStarting VMState = 4
	VMStatePausing  VMState = 5
	VMStateResuming VMState = 6
	VMStateStopping VMState = 7
)

func (s VMState) String() string {
	switch s {
	case VMStateStopped:
		return "stopped"
	case VMStateRunning:
		return "running"
	case VMStatePaused:
		return "paused"
	case VMStateError:
		return "error"
	case VMStateStarting:
		return "starting"
	case VMStatePausing:
		return "pausing"
	case VMStateResuming:
		return "resuming"
	case VMStateStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

type VM struct {
	Name         string
	Config       VMConfig
	vmHandle     unsafe.Pointer
	mu           sync.Mutex
	stdinWriter  *os.File // host writes here -> guest reads
	stdoutReader *os.File // guest writes -> host reads here
}

var (
	startResultCh = make(chan error, 1)
	stopResultCh  = make(chan error, 1)
	vmStoppedCh   = make(chan error, 1)
)

//export goVMStartCallback
func goVMStartCallback(success C.bool, errMsg *C.char) {
	if bool(success) {
		startResultCh <- nil
	} else {
		startResultCh <- errors.New(C.GoString(errMsg))
	}
}

//export goVMStopCallback
func goVMStopCallback(success C.bool, errMsg *C.char) {
	if bool(success) {
		stopResultCh <- nil
	} else {
		stopResultCh <- errors.New(C.GoString(errMsg))
	}
}

//export goVMDidStop
func goVMDidStop() {
	select {
	case vmStoppedCh <- nil:
	default:
	}
}

//export goVMDidStopWithError
func goVMDidStopWithError(errMsg *C.char) {
	select {
	case vmStoppedCh <- errors.New(C.GoString(errMsg)):
	default:
	}
}

func NewVM(name string, cfg VMConfig) (*VM, error) {
	var errOut *C.char

	// Boot loader
	kernelPath := C.CString(cfg.KernelPath)
	defer C.free(unsafe.Pointer(kernelPath))
	cmdLine := C.CString(cfg.CommandLine)
	defer C.free(unsafe.Pointer(cmdLine))

	var initrdPath *C.char
	if cfg.InitrdPath != "" {
		initrdPath = C.CString(cfg.InitrdPath)
		defer C.free(unsafe.Pointer(initrdPath))
	} else {
		initrdPath = C.CString("")
		defer C.free(unsafe.Pointer(initrdPath))
	}

	bootLoader := C.createLinuxBootLoader(kernelPath, cmdLine, initrdPath, &errOut)
	if bootLoader == nil {
		defer C.free(unsafe.Pointer(errOut))
		return nil, fmt.Errorf("boot loader: %s", C.GoString(errOut))
	}

	// Storage Devices
	var storageDevices []unsafe.Pointer

	// Disk (optional)
	if cfg.DiskPath != "" {
		diskPath := C.CString(cfg.DiskPath)
		defer C.free(unsafe.Pointer(diskPath))
		diskAtt := C.createDiskImageAttachment(diskPath, C.bool(false), &errOut)
		if diskAtt == nil {
			defer C.free(unsafe.Pointer(errOut))
			return nil, fmt.Errorf("disk attachment: %s", C.GoString(errOut))
		}
		storageDevices = append(storageDevices, C.createVirtioBlockDevice(diskAtt))
	}

	// ISO (optional, read-only)
	if cfg.ISOPath != "" {
		isoPath := C.CString(cfg.ISOPath)
		defer C.free(unsafe.Pointer(isoPath))
		isoAtt := C.createDiskImageAttachment(isoPath, C.bool(true), &errOut)
		if isoAtt == nil {
			defer C.free(unsafe.Pointer(errOut))
			return nil, fmt.Errorf("ISO attachment: %s", C.GoString(errOut))
		}
		storageDevices = append(storageDevices, C.createVirtioBlockDevice(isoAtt))
	}

	// Network
	natAtt := C.createNATAttachment()
	netDev := C.createVirtioNetworkDevice(natAtt)
	networkDevices := [1]unsafe.Pointer{netDev}

	// Serial Console using pipes
	// Guest reads from stdinReader (host writes to stdinWriter)
	// Guest writes to stdoutWriter (host reads from stdoutReader)
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	serialAtt := C.createFileHandleSerialPortAttachment(
		C.int(stdinReader.Fd()),
		C.int(stdoutWriter.Fd()),
	)
	serialPort := C.createVirtioConsoleSerialPort(serialAtt)
	serialPorts := [1]unsafe.Pointer{serialPort}

	// Entropy
	entropy := C.createVirtioEntropyDevice()

	// Memory Balloon
	balloon := C.createMemoryBalloonDevice()

	// Directory Sharing (VirtioFS)
	var dirShareDevices []unsafe.Pointer
	for _, mount := range cfg.Mounts {
		tag := C.CString(mount.Tag)
		hostPath := C.CString(mount.HostPath)
		fsDev := C.createVirtioFileSystemDevice(tag, hostPath, C.bool(mount.ReadOnly), &errOut)
		C.free(unsafe.Pointer(tag))
		C.free(unsafe.Pointer(hostPath))
		if fsDev == nil {
			defer C.free(unsafe.Pointer(errOut))
			return nil, fmt.Errorf("mount '%s': %s", mount.Tag, C.GoString(errOut))
		}
		dirShareDevices = append(dirShareDevices, fsDev)
	}

	var dirSharePtr *unsafe.Pointer
	if len(dirShareDevices) > 0 {
		dirSharePtr = (*unsafe.Pointer)(unsafe.Pointer(&dirShareDevices[0]))
	}

	var storagePtr *unsafe.Pointer
	if len(storageDevices) > 0 {
		storagePtr = (*unsafe.Pointer)(unsafe.Pointer(&storageDevices[0]))
	}

	// Assemble VM Configuration
	vmConfig := C.createVMConfig(
		bootLoader,
		C.uint(cfg.CPUCount),
		C.uint64_t(cfg.MemoryBytes),
		storagePtr, C.int(len(storageDevices)),
		(*unsafe.Pointer)(unsafe.Pointer(&networkDevices[0])), C.int(1),
		(*unsafe.Pointer)(unsafe.Pointer(&serialPorts[0])), C.int(1),
		entropy,
		balloon,
		dirSharePtr, C.int(len(dirShareDevices)),
		&errOut,
	)
	if vmConfig == nil {
		defer C.free(unsafe.Pointer(errOut))
		return nil, fmt.Errorf("vm config: %s", C.GoString(errOut))
	}

	// Create VM
	vmHandle := C.createVM(vmConfig)

	return &VM{
		Name:         name,
		Config:       cfg,
		vmHandle:     vmHandle,
		stdinWriter:  stdinWriter,
		stdoutReader: stdoutReader,
	}, nil
}

// StartIORelay starts goroutines that relay between the VM serial console
// and the provided reader/writer (typically os.Stdin and os.Stdout).
func (vm *VM) StartIORelay(input io.Reader, output io.Writer) {
	// Guest stdout -> host output
	go io.Copy(output, vm.stdoutReader)
	// Host input -> guest stdin
	go io.Copy(vm.stdinWriter, input)
}

func (vm *VM) Start() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	C.startVM(vm.vmHandle)
	return <-startResultCh
}

func (vm *VM) Stop() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	C.stopVM(vm.vmHandle)
	return <-stopResultCh
}

func (vm *VM) RequestStop() {
	C.requestStopVM(vm.vmHandle)
}

func (vm *VM) State() VMState {
	return VMState(C.getVMState(vm.vmHandle))
}

func (vm *VM) WaitUntilStopped() error {
	return <-vmStoppedCh
}

func RunMainRunLoop() {
	C.runMainRunLoop()
}

func StopMainRunLoop() {
	C.stopMainRunLoop()
}

func MaxCPUCount() uint {
	return uint(C.vzMaxCPUCount())
}

func MinCPUCount() uint {
	return uint(C.vzMinCPUCount())
}

func MaxMemorySize() uint64 {
	return uint64(C.vzMaxMemorySize())
}

func MinMemorySize() uint64 {
	return uint64(C.vzMinMemorySize())
}
