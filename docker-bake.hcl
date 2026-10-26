variable "IMAGE_PREFIX" {
  default = "deskyard-"
}
variable "VERSION" {
  default = "dev"
}
group "default" {
  targets = ["debian-xfce"]
}
target "_common" {
  context = "."
  dockerfile = "images/Dockerfile"
  args = { VERSION = VERSION }
}
target "debian-xfce" {
  inherits = ["_common"]
  target = "debian-xfce"
  tags = ["${IMAGE_PREFIX}debian-xfce:${VERSION}"]
}
