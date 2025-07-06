# Dagger SDK directive for setting default paths
directive @defaultPath(path: String!, ignore: [String!] = []) on ARGUMENT_DEFINITION

type Bind {
  pub source: Directory! @defaultPath(
    path: "/",
    ignore: [
      # TODO: respecting .gitignore would be nice
      "Session.vim"
      "/bind"
      "/zed-bind/grammars/",
      "/.env"
    ]
  )

  pub build: File! {
    go(source).binary("./cmd/bind")
  }

  pub test: Container! {
    go(source).base.
      withDirectory("/src", source).
      withWorkdir("/src").
      withExec(["go", "test", "-v", "./..."], experimentalPrivilegedNesting: true)
  }
}
