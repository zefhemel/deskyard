/* Link against the same libglnx objects built into Flatpak. A one-shot
 * openat2 failure must be retried without relaxing directory confinement. */
#include <errno.h>
#include <fcntl.h>
#include <linux/openat2.h>
#include <linux/stat.h>
#include <stdarg.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <unistd.h>
#include "libglnx.h"

static int fault;
static unsigned calls;
long __real_syscall(long number, ...);

long __wrap_syscall(long number, ...)
{
    va_list args;
    va_start(args, number);
    if (number == SYS_statx) {
        int dirfd = va_arg(args, int);
        const char *path = va_arg(args, const char *);
        int flags = va_arg(args, int);
        unsigned mask = va_arg(args, unsigned);
        struct statx *stat = va_arg(args, struct statx *);
        va_end(args);
        return __real_syscall(number, dirfd, path, flags, mask, stat);
    }
    if (number != SYS_openat2) {
        va_end(args);
        fprintf(stderr, "unexpected syscall %ld\n", number);
        _exit(2);
    }
    int dirfd = va_arg(args, int);
    const char *path = va_arg(args, const char *);
    const struct open_how *how = va_arg(args, const struct open_how *);
    size_t size = va_arg(args, size_t);
    va_end(args);
    calls++;
    if (fault) {
        errno = fault;
        fault = 0;
        return -1;
    }
    return __real_syscall(number, dirfd, path, how, size);
}

int main(void)
{
    int flags = GLNX_CHASE_RESOLVE_BENEATH | GLNX_CHASE_MUST_BE_DIRECTORY;
    fault = EAGAIN;
    int fd = glnx_chaseat(AT_FDCWD, ".", flags, NULL);
    if (fd < 0 || calls < 2) {
        fprintf(stderr, "EAGAIN was not retried: fd=%d errno=%d calls=%u\n", fd, errno, calls);
        return 1;
    }
    close(fd);
    calls = 0;
    fault = EACCES;
    fd = glnx_chaseat(AT_FDCWD, ".", flags, NULL);
    if (fd >= 0 || errno != EACCES || calls != 1) {
        fprintf(stderr, "permission failure was not preserved\n");
        return 1;
    }
    fd = glnx_chaseat(AT_FDCWD, "..", flags, NULL);
    if (fd >= 0) {
        fprintf(stderr, "RESOLVE_BENEATH allowed escape to parent\n");
        return 1;
    }
    puts("Flatpak openat2 retry and confinement checks passed");
    return 0;
}
