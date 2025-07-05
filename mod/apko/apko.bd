pub description =
  "Builds containers from simple lists of packages using the Apko CLI."

# TODO
let arch = "x86_64"

type Apko {
  let config = {{
    contents: {{
      packages: []::[String!]!
      repositories: []::[String!]!
      keyring: []::[String!]!
    }}
    cmd: ""
    environment: {{ # TODO: how to support arbitrary key-value?
      PATH: "/usr/local/bin:/usr/bin:/bin"
    }}
    archs: [arch]
  }}

  pub withAlpine(branch: String! = "edge"): Apko! {
    self.config.contents.packages += ["apk-tools"]
    self.config.contents.repositories += [
      "https://dl-cdn.alpinelinux.org/alpine/" + branch + "/main"
    ]
    self
  }

  pub withPackages(packages: [String!]!): Apko! {
    self.config.contents.packages += packages
    self
  }

  pub withArchs(archs: [String!]!): Apko! {
    self.config.archs += archs
    self
  }

  pub asContainer: Container! {
    container.import(
      container.
        from("cgr.dev/chainguard/apko").
        withMountedCache("/apkache/", cacheVolume("apko")).
        withNewFile("/config.yml", contents: json(config)).
        withExec(["apko", "build", "--cache-dir", "/apkache/", "/config.yml", "latest","layout.tar"]).
        file("layout.tar")
    )
  }

  pub alpine(packages: [String!]!, branch: String! = "edge"): Container! {
    withAlpine(branch).withPackages(packages).asContainer
  }
}

# USAGE:
# print(json(Apko.withAlpine.config))
# print(Apko.withAlpine.withPackages(["git"]).asContainer.withExec(["git", "version"]).stdout)
# print(Apko.alpine(["git"]).withExec(["git", "version"]).stdout)
