assert("auto-calling symbols") {
  serverInfo().platform() == serverInfo.platform()
}

assert("auto-calling selections") {
  serverInfo.platform == serverInfo.platform()
}
