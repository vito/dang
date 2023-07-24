{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        /* defaultPackage = naersk-lib.buildPackage ./.; */

        /* defaultApp = utils.lib.mkApp { */
        /*   drv = self.defaultPackage."${system}"; */
        /* }; */

        devShell = with pkgs; mkShell {
          buildInputs = [
            tree-sitter
            nodejs
            nodePackages.npm
            nodePackages.typescript
            nodePackages.typescript-language-server
            python3 # for gnode-yp
          ];
        };
      });
}
