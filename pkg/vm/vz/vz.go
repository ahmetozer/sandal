//go:build darwin

package vz

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Virtualization -framework Foundation

#include <stdlib.h>
#include "vz.h"
*/
import "C"
