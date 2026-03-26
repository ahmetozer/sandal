// Pre-init stub for ARM64 KVM VMs.
// Mounts /proc and /dev, opens /dev/console for stdin/stdout/stderr,
// then execve's /sandal-init (the actual Go binary).
// This is needed because Go's runtime requires /proc and valid fds.
//
// Build: aarch64-linux-gnu-as -o preinit_arm64.o preinit_arm64.s && \
//        aarch64-linux-gnu-ld -static -o preinit_arm64.bin preinit_arm64.o && \
//        rm preinit_arm64.o
.global _start
_start:
    // mkdir /proc
    mov x8, #34        // mkdirat
    mov x0, #-100      // AT_FDCWD
    adr x1, proc_path
    mov x2, #0755
    svc #0
    // mount proc
    mov x8, #40         // mount
    adr x0, proc_str
    adr x1, proc_path
    adr x2, proc_str
    mov x3, #0
    mov x4, #0
    svc #0
    // mkdir /dev
    mov x8, #34
    mov x0, #-100
    adr x1, dev_path
    mov x2, #0755
    svc #0
    // mount devtmpfs
    mov x8, #40
    adr x0, devtmpfs_str
    adr x1, dev_path
    adr x2, devtmpfs_str
    mov x3, #0
    mov x4, #0
    svc #0
    // open /dev/console O_RDWR
    mov x8, #56         // openat
    mov x0, #-100       // AT_FDCWD
    adr x1, console_path
    mov x2, #2          // O_RDWR
    mov x3, #0
    svc #0
    mov x19, x0         // save fd
    // dup3 to fd 0 (stdin)
    mov x8, #24         // dup3
    mov x1, #0
    mov x2, #0
    svc #0
    // dup3 to fd 1 (stdout)
    mov x8, #24
    mov x0, x19
    mov x1, #1
    mov x2, #0
    svc #0
    // dup3 to fd 2 (stderr)
    mov x8, #24
    mov x0, x19
    mov x1, #2
    mov x2, #0
    svc #0
    // execve /sandal-init with argv=["/sandal-init", NULL] envp=NULL
    mov x8, #221        // execve
    adr x0, sandal_path
    adr x1, argv
    mov x2, #0          // envp = NULL
    svc #0
    // execve failed — loop forever
1:  b 1b

.balign 8
sandal_path: .asciz "/sandal-init"
proc_path: .asciz "/proc"
proc_str: .asciz "proc"
dev_path: .asciz "/dev"
devtmpfs_str: .asciz "devtmpfs"
console_path: .asciz "/dev/console"
.balign 8
argv:
    .quad sandal_path
    .quad 0
