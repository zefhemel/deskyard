/* Verify the Libretro ABI from inside the same Flatpak runtime as RetroArch. */
#include <dlfcn.h>
#include <stdbool.h>
#include <stdio.h>

struct retro_system_info {
    const char *library_name;
    const char *library_version;
    const char *valid_extensions;
    bool need_fullpath;
    bool block_extract;
};

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: verify-cores CORE.so ...\n");
        return 2;
    }
    for (int i = 1; i < argc; i++) {
        void *core = dlopen(argv[i], RTLD_NOW | RTLD_LOCAL);
        if (!core) {
            fprintf(stderr, "%s: %s\n", argv[i], dlerror());
            return 1;
        }
        unsigned (*api_version)(void) = dlsym(core, "retro_api_version");
        void (*get_info)(struct retro_system_info *) = dlsym(core, "retro_get_system_info");
        if (!api_version || !get_info || api_version() != 1) {
            fprintf(stderr, "%s: missing or incompatible Libretro API\n", argv[i]);
            return 1;
        }
        struct retro_system_info info = {0};
        get_info(&info);
        if (!info.library_name || !info.library_version || !info.valid_extensions) {
            fprintf(stderr, "%s: incomplete system information\n", argv[i]);
            return 1;
        }
        printf("%s: %s %s [%s]\n", argv[i], info.library_name,
               info.library_version, info.valid_extensions);
        dlclose(core);
    }
    return 0;
}
