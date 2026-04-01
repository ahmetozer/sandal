#ifndef VZ_H
#define VZ_H

#include <stdbool.h>
#include <stdint.h>

// Boot Loader
void* createLinuxBootLoader(const char *kernelPath, const char *cmdLine, const char *initrdPath, char **errOut);

// Disk
void* createDiskImageAttachment(const char *diskPath, bool readOnly, char **errOut);
void* createVirtioBlockDevice(void *attachment);

// Network
void* createNATAttachment(void);
void* createVirtioNetworkDevice(void *natAttachment);

// Serial Console
void* createFileHandleSerialPortAttachment(int readFD, int writeFD);
void* createVirtioConsoleSerialPort(void *attachment);

// Entropy
void* createVirtioEntropyDevice(void);

// Memory Balloon
void* createMemoryBalloonDevice(void);

// Directory Sharing (VirtioFS)
void* createVirtioFileSystemDevice(const char *tag, const char *dirPath, bool readOnly, char **errOut);

// Vsock (virtio socket for host<->guest communication)
void* createVirtioSocketDevice(void);
void vzSocketListen(void *vmHandle, uint32_t port);

// VM Configuration
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
);

// VM Lifecycle
void* createVM(void *config);
void startVM(void *vmHandle);
void stopVM(void *vmHandle);
void requestStopVM(void *vmHandle);
int getVMState(void *vmHandle);

// Run Loop
void runMainRunLoop(void);
void stopMainRunLoop(void);

// Utility
unsigned int vzMaxCPUCount(void);
unsigned int vzMinCPUCount(void);
uint64_t vzMaxMemorySize(void);
uint64_t vzMinMemorySize(void);

// Memory Management
void releaseHandle(void *handle);

#endif
