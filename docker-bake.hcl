variable "IMAGE_PREFIX" {
  default = "deskyard-"
}
variable "VERSION" {
  default = "dev"
}
group "default" {
  targets = ["debian-xfce", "debian-kde", "debian-mate"]
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
target "debian-kde" {
  inherits = ["_common"]
  target = "debian-kde"
  tags = ["${IMAGE_PREFIX}debian-kde:${VERSION}"]
}
target "debian-mate" {
  inherits = ["_common"]
  target = "debian-mate"
  tags = ["${IMAGE_PREFIX}debian-mate:${VERSION}"]
}
