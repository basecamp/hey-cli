{ fetchurl, go_1_27 }:

# Both the package and dev shell use the patched compiler. The locked nixpkgs
# still supplies 1.27.3, so override its source until it includes this security fix.
go_1_27.overrideAttrs (finalAttrs: _: {
  version = "1.27.4";
  src = fetchurl {
    url = "https://go.dev/dl/go${finalAttrs.version}.src.tar.gz";
    hash = "sha256-c8+4VxrsDAEXdofbhxY9Rqog6XeWcncokaoXZU2aYJk=";
  };
})
