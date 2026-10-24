# Docker desktop example

This folder is a complete distributable build context. It installs Debian's Docker CLI and Buildx into a Deskyard desktop, and seeds a small build example in a fresh workspace. It runs against the host's daemon through the **Docker daemon access** checkbox; there is no nested daemon.

Build outside the supervisor, then add the locally built tag on its Images page:

```sh
docker build -t deskyard-docker:example .
# Or select a locally built base that includes Docker access startup support:
docker build --build-arg BASE_IMAGE=deskyard-debian-xfce:dev -t deskyard-docker:example .
```

Create a desktop from `deskyard-docker:example` and enable **Docker daemon access**. The setting defaults off and changing it recreates the desktop. It requires a reachable daemon-side `/var/run/docker.sock` and a base image with Deskyard's socket-group startup support. Installing the CLI alone does not grant access.

Anyone with shell or application access to an enabled desktop can control the shared Docker daemon, including other containers and host mounts. Give this permission only to desktops whose users you trust as host administrators. The supervisor leaves socket ownership and mode unchanged.

Inside the desktop, as `user`:

```sh
cd ~/docker-demo
sh build-and-run.sh
```

This sends the local Dockerfile and `message.txt` to the daemon, builds a uniquely tagged image, runs it, checks its output and removes that tag. The initial Alpine base pull needs network access. The expected output is `Hello from an image built inside a Deskyard desktop!`.

Containers launched this way are siblings on the host daemon. A bind source in `docker run -v /some/path:/data` refers to the daemon host, not the desktop's filesystem. Build contexts transfer normally; use named volumes or `docker cp` when sharing files with a child container.

The example uses [Debian docker-cli](https://packages.debian.org/trixie/docker-cli) and [docker-buildx](https://packages.debian.org/trixie/docker-buildx). Pin the base image by digest and package versions if you need reproducible releases. Base image licenses and the installed packages' copyright files remain in the resulting image.
