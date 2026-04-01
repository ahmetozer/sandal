#import <Virtualization/Virtualization.h>
#import <Foundation/Foundation.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdlib.h>

// Forward declarations for Go-exported callbacks
extern void goVMDidStop(void);
extern void goVMDidStopWithError(const char *err);
extern void goVMStartCallback(bool success, const char *err);
extern void goVMStopCallback(bool success, const char *err);
extern void goVsockNewConnection(uint32_t port, int fd);

// --- Delegate ---

@interface SandalVMDelegate : NSObject <VZVirtualMachineDelegate>
@end

@implementation SandalVMDelegate

- (void)guestDidStopVirtualMachine:(VZVirtualMachine *)virtualMachine {
    goVMDidStop();
}

- (void)virtualMachine:(VZVirtualMachine *)virtualMachine didStopWithError:(NSError *)error {
    goVMDidStopWithError([[error localizedDescription] UTF8String]);
}

- (void)observeValueForKeyPath:(NSString *)keyPath
                      ofObject:(id)object
                        change:(NSDictionary<NSKeyValueChangeKey,id> *)change
                       context:(void *)context {
    if ([keyPath isEqualToString:@"state"]) {
        VZVirtualMachineState newState = (VZVirtualMachineState)[change[NSKeyValueChangeNewKey] integerValue];
        if (newState == VZVirtualMachineStateStopped) {
            goVMDidStop();
        }
    }
}

@end

static SandalVMDelegate *sharedDelegate = nil;

// --- Boot Loader ---

void* createLinuxBootLoader(const char *kernelPath, const char *cmdLine, const char *initrdPath, char **errOut) {
    @autoreleasepool {
        NSURL *kernelURL = [NSURL fileURLWithPath:@(kernelPath)];
        VZLinuxBootLoader *bl = [[VZLinuxBootLoader alloc] initWithKernelURL:kernelURL];
        if (cmdLine) {
            bl.commandLine = @(cmdLine);
        }
        if (initrdPath && strlen(initrdPath) > 0) {
            bl.initialRamdiskURL = [NSURL fileURLWithPath:@(initrdPath)];
        }
        return (__bridge_retained void *)bl;
    }
}

// --- Disk ---

void* createDiskImageAttachment(const char *diskPath, bool readOnly, char **errOut) {
    @autoreleasepool {
        NSError *error = nil;
        NSURL *url = [NSURL fileURLWithPath:@(diskPath)];
        VZDiskImageStorageDeviceAttachment *att =
            [[VZDiskImageStorageDeviceAttachment alloc] initWithURL:url readOnly:readOnly error:&error];
        if (error) {
            *errOut = strdup([[error localizedDescription] UTF8String]);
            return NULL;
        }
        return (__bridge_retained void *)att;
    }
}

void* createVirtioBlockDevice(void *attachment) {
    VZStorageDeviceAttachment *att = (__bridge VZStorageDeviceAttachment *)attachment;
    VZVirtioBlockDeviceConfiguration *block =
        [[VZVirtioBlockDeviceConfiguration alloc] initWithAttachment:att];
    return (__bridge_retained void *)block;
}

// --- Network ---

void* createNATAttachment(void) {
    VZNATNetworkDeviceAttachment *nat = [[VZNATNetworkDeviceAttachment alloc] init];
    return (__bridge_retained void *)nat;
}

void* createVirtioNetworkDevice(void *natAttachment) {
    VZVirtioNetworkDeviceConfiguration *net = [[VZVirtioNetworkDeviceConfiguration alloc] init];
    net.attachment = (__bridge VZNetworkDeviceAttachment *)natAttachment;
    net.MACAddress = [VZMACAddress randomLocallyAdministeredAddress];
    return (__bridge_retained void *)net;
}

// --- Serial Console ---

void* createFileHandleSerialPortAttachment(int readFD, int writeFD) {
    NSFileHandle *readHandle = [[NSFileHandle alloc] initWithFileDescriptor:readFD closeOnDealloc:NO];
    NSFileHandle *writeHandle = [[NSFileHandle alloc] initWithFileDescriptor:writeFD closeOnDealloc:NO];
    VZFileHandleSerialPortAttachment *att =
        [[VZFileHandleSerialPortAttachment alloc] initWithFileHandleForReading:readHandle
                                                          fileHandleForWriting:writeHandle];
    return (__bridge_retained void *)att;
}

void* createVirtioConsoleSerialPort(void *attachment) {
    VZVirtioConsoleDeviceSerialPortConfiguration *serial =
        [[VZVirtioConsoleDeviceSerialPortConfiguration alloc] init];
    serial.attachment = (__bridge VZSerialPortAttachment *)attachment;
    return (__bridge_retained void *)serial;
}

// --- Entropy ---

void* createVirtioEntropyDevice(void) {
    VZVirtioEntropyDeviceConfiguration *entropy = [[VZVirtioEntropyDeviceConfiguration alloc] init];
    return (__bridge_retained void *)entropy;
}

// --- Memory Balloon ---

void* createMemoryBalloonDevice(void) {
    VZVirtioTraditionalMemoryBalloonDeviceConfiguration *balloon =
        [[VZVirtioTraditionalMemoryBalloonDeviceConfiguration alloc] init];
    return (__bridge_retained void *)balloon;
}

// --- Directory Sharing (VirtioFS) ---

void* createVirtioFileSystemDevice(const char *tag, const char *dirPath, bool readOnly, char **errOut) {
    @autoreleasepool {
        NSURL *dirURL = [NSURL fileURLWithPath:@(dirPath) isDirectory:YES];
        VZSharedDirectory *sharedDir = [[VZSharedDirectory alloc] initWithURL:dirURL readOnly:readOnly];
        VZSingleDirectoryShare *share = [[VZSingleDirectoryShare alloc] initWithDirectory:sharedDir];

        VZVirtioFileSystemDeviceConfiguration *fsConfig =
            [[VZVirtioFileSystemDeviceConfiguration alloc] initWithTag:@(tag)];
        fsConfig.share = share;

        return (__bridge_retained void *)fsConfig;
    }
}

// --- Vsock ---

@interface SandalVsockListenerDelegate : NSObject <VZVirtioSocketListenerDelegate>
@end

@implementation SandalVsockListenerDelegate

- (BOOL)listener:(VZVirtioSocketListener *)listener
    shouldAcceptNewConnection:(VZVirtioSocketConnection *)connection
    fromSocketDevice:(VZVirtioSocketDevice *)socketDevice {
    // Get the fd from the connection and hand it to Go for relay.
    // The connection's fileDescriptor is valid once we accept.
    int fd = dup((int)connection.fileDescriptor);
    uint32_t port = (uint32_t)connection.destinationPort;
    dispatch_async(dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT, 0), ^{
        goVsockNewConnection(port, fd);
    });
    return YES;
}

@end

static SandalVsockListenerDelegate *sharedVsockDelegate = nil;

void* createVirtioSocketDevice(void) {
    VZVirtioSocketDeviceConfiguration *socketConfig =
        [[VZVirtioSocketDeviceConfiguration alloc] init];
    return (__bridge_retained void *)socketConfig;
}

void vzSocketListen(void *vmHandle, uint32_t port) {
    dispatch_async(dispatch_get_main_queue(), ^{
        VZVirtualMachine *vm = (__bridge VZVirtualMachine *)vmHandle;

        // Find the vsock device from the VM's socket devices
        NSArray *socketDevices = vm.socketDevices;
        if (socketDevices.count == 0) {
            return;
        }
        VZVirtioSocketDevice *dev = (VZVirtioSocketDevice *)socketDevices[0];

        if (!sharedVsockDelegate) {
            sharedVsockDelegate = [[SandalVsockListenerDelegate alloc] init];
        }

        VZVirtioSocketListener *listener = [[VZVirtioSocketListener alloc] init];
        listener.delegate = sharedVsockDelegate;
        [dev setSocketListener:listener forPort:port];
    });
}

// --- VM Configuration ---

void* createVMConfig(
    void *bootLoader,
    unsigned int cpuCount,
    uint64_t memorySize,
    void **storageDevices, int storageCount,
    void **networkDevices, int networkCount,
    void **serialPorts, int serialCount,
    void *entropyDevice,
    void *memoryBalloon,
    void **dirShareDevices, int dirShareCount,
    void *socketDevice,
    char **errOut
) {
    @autoreleasepool {
        VZVirtualMachineConfiguration *config = [[VZVirtualMachineConfiguration alloc] init];
        config.bootLoader = (__bridge VZBootLoader *)bootLoader;
        config.CPUCount = cpuCount;
        config.memorySize = memorySize;

        // Storage
        if (storageDevices && storageCount > 0) {
            NSMutableArray *storage = [NSMutableArray arrayWithCapacity:storageCount];
            for (int i = 0; i < storageCount; i++) {
                [storage addObject:(__bridge VZStorageDeviceConfiguration *)storageDevices[i]];
            }
            config.storageDevices = storage;
        }

        // Network
        NSMutableArray *network = [NSMutableArray arrayWithCapacity:networkCount];
        for (int i = 0; i < networkCount; i++) {
            [network addObject:(__bridge VZNetworkDeviceConfiguration *)networkDevices[i]];
        }
        config.networkDevices = network;

        // Serial
        NSMutableArray *serial = [NSMutableArray arrayWithCapacity:serialCount];
        for (int i = 0; i < serialCount; i++) {
            [serial addObject:(__bridge VZSerialPortConfiguration *)serialPorts[i]];
        }
        config.serialPorts = serial;

        // Entropy
        if (entropyDevice) {
            config.entropyDevices = @[(__bridge VZEntropyDeviceConfiguration *)entropyDevice];
        }

        // Memory Balloon
        if (memoryBalloon) {
            config.memoryBalloonDevices = @[(__bridge VZMemoryBalloonDeviceConfiguration *)memoryBalloon];
        }

        // Directory Sharing (VirtioFS)
        if (dirShareDevices && dirShareCount > 0) {
            NSMutableArray *dirShares = [NSMutableArray arrayWithCapacity:dirShareCount];
            for (int i = 0; i < dirShareCount; i++) {
                [dirShares addObject:(__bridge VZDirectorySharingDeviceConfiguration *)dirShareDevices[i]];
            }
            config.directorySharingDevices = dirShares;
        }

        // Vsock
        if (socketDevice) {
            config.socketDevices = @[(__bridge VZVirtioSocketDeviceConfiguration *)socketDevice];
        }

        // Validate
        NSError *error = nil;
        if (![config validateWithError:&error]) {
            *errOut = strdup([[error localizedDescription] UTF8String]);
            return NULL;
        }

        return (__bridge_retained void *)config;
    }
}

// --- VM Lifecycle ---
// VZVirtualMachine requires all operations on the main dispatch queue.
// We use dispatch_async to ensure thread safety from any Go goroutine.

// createVM must be called from the main thread (via runtime.LockOSThread).
void* createVM(void *config) {
    VZVirtualMachineConfiguration *c = (__bridge VZVirtualMachineConfiguration *)config;
    VZVirtualMachine *vm = [[VZVirtualMachine alloc] initWithConfiguration:c];

    if (!sharedDelegate) {
        sharedDelegate = [[SandalVMDelegate alloc] init];
    }
    vm.delegate = sharedDelegate;

    [vm addObserver:sharedDelegate
         forKeyPath:@"state"
            options:NSKeyValueObservingOptionNew
            context:NULL];

    return (__bridge_retained void *)vm;
}

void startVM(void *vmHandle) {
    dispatch_async(dispatch_get_main_queue(), ^{
        VZVirtualMachine *v = (__bridge VZVirtualMachine *)vmHandle;
        [v startWithCompletionHandler:^(NSError *err) {
            if (err) {
                NSString *detail = [NSString stringWithFormat:@"%@ (domain=%@ code=%ld)",
                    [err localizedDescription], [err domain], (long)[err code]];
                if ([err userInfo]) {
                    detail = [detail stringByAppendingFormat:@" userInfo=%@", [err userInfo]];
                }
                goVMStartCallback(false, [detail UTF8String]);
            } else {
                goVMStartCallback(true, NULL);
            }
        }];
    });
}

void stopVM(void *vmHandle) {
    dispatch_async(dispatch_get_main_queue(), ^{
        VZVirtualMachine *v = (__bridge VZVirtualMachine *)vmHandle;
        [v stopWithCompletionHandler:^(NSError *err) {
            if (err) {
                goVMStopCallback(false, [[err localizedDescription] UTF8String]);
            } else {
                goVMStopCallback(true, NULL);
            }
        }];
    });
}

void requestStopVM(void *vmHandle) {
    dispatch_async(dispatch_get_main_queue(), ^{
        VZVirtualMachine *v = (__bridge VZVirtualMachine *)vmHandle;
        NSError *error = nil;
        [v requestStopWithError:&error];
    });
}

int getVMState(void *vmHandle) {
    VZVirtualMachine *v = (__bridge VZVirtualMachine *)vmHandle;
    return (int)v.state;
}

// --- Run Loop ---

static BOOL shouldStopRunLoop = NO;

void runMainRunLoop(void) {
    shouldStopRunLoop = NO;
    // Use CFRunLoop which integrates with both NSRunLoop and GCD main queue.
    // dispatch_async(dispatch_get_main_queue()) blocks are processed here.
    while (!shouldStopRunLoop) {
        CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, true);
    }
}

void stopMainRunLoop(void) {
    shouldStopRunLoop = YES;
    // Wake the run loop so it exits promptly
    CFRunLoopStop(CFRunLoopGetMain());
}

// --- Utility ---

unsigned int vzMaxCPUCount(void) {
    return (unsigned int)VZVirtualMachineConfiguration.maximumAllowedCPUCount;
}

unsigned int vzMinCPUCount(void) {
    return (unsigned int)VZVirtualMachineConfiguration.minimumAllowedCPUCount;
}

uint64_t vzMaxMemorySize(void) {
    return VZVirtualMachineConfiguration.maximumAllowedMemorySize;
}

uint64_t vzMinMemorySize(void) {
    return VZVirtualMachineConfiguration.minimumAllowedMemorySize;
}

// --- Memory Management ---

void releaseHandle(void *handle) {
    if (handle) {
        CFRelease(handle);
    }
}
