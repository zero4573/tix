{ pkgs, lib, env ? { } }:
let
  inherit (pkgs.stdenv.hostPlatform) isLinux;

  ctHook = pkgs.runCommand "tix-hook" { nativeBuildInputs = [ pkgs.makeWrapper ]; } ''
    makeWrapper ${lib.getExe tixBin} $out/bin/tix \
      ${lib.optionalString isLinux "--suffix PATH : ${lib.makeBinPath [ pkgs.libnotify ]}"}
  '';
  plugin = pkgs.runCommand "tix-plugin" { } ''
    cp -r ${../assets/plugin} $out
    chmod -R u+w $out
    substituteInPlace $out/hooks/hooks.json \
      --replace-fail '"tix hook ' '"${ctHook}/bin/tix hook '
  '';

  version = "0.1.0";
  docsCheckedByAssetsTests = [ ../docs ../README.md ../AGENTS.md ];
  tixBin = pkgs.buildGoModule {
    pname = "tix";
    inherit version;
    src = lib.fileset.toSource {
      root = ../.;
      fileset = lib.fileset.unions ([
        ../go.mod ../go.sum ../cmd ../internal ../assets ../tix_test.go ../tix_sync_test.go ../testdata
      ] ++ docsCheckedByAssetsTests);
    };
    vendorHash = "sha256-mTX4/p9otTdny5gQRO/jmlv0TS9w4BhWv49YXVCEohA=";
    subPackages = [ "cmd/tix" ];
    ldflags = [ "-s" "-w" "-X github.com/zero4573/tix/internal/version.Version=${version}" ]
      ++ lib.optional isLinux "-X github.com/zero4573/tix/internal/embed.DefaultLibPath=${lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib ]}";
    nativeCheckInputs = with pkgs; [ coreutils git openssh ];
    checkPhase = ''
      runHook preCheck
      go test ./...
      runHook postCheck
    '';
    meta.mainProgram = "tix";
  };

  defaultsEnv = { TIX_PLUGIN = plugin; } // env;

  tools = with pkgs; [ git coreutils fzf bash gitleaks gosec govulncheck syft ] ++ lib.optional isLinux pkgs.libnotify;

  tix = pkgs.runCommand "tix"
    {
      nativeBuildInputs = [ pkgs.makeWrapper ];
      meta.mainProgram = "tix";
    } ''
    mkdir -p $out/bin
    makeWrapper ${lib.getExe tixBin} $out/bin/tix \
      ${lib.concatStrings (lib.mapAttrsToList (k: v: "--set-default ${k} ${lib.escapeShellArg (toString v)} ") defaultsEnv)} \
      --suffix PATH : ${lib.makeBinPath tools}
    mkdir -p $out/share/zsh/site-functions $out/share/bash-completion/completions $out/share/fish/vendor_completions.d
    ${lib.getExe tixBin} completion zsh > $out/share/zsh/site-functions/_tix
    ${lib.getExe tixBin} completion bash > $out/share/bash-completion/completions/tix
    ${lib.getExe tixBin} completion fish > $out/share/fish/vendor_completions.d/tix.fish
  '';
  llamaRuntimeLibs = pkgs.runCommand "llama-runtime-libs" { } ''
    mkdir -p $out
    ln -s ${pkgs.stdenv.cc.cc.lib}/lib/libstdc++.so.6 $out/
    ln -s ${lib.getLib pkgs.stdenv.cc.cc}/lib/libgomp.so.1 $out/
  '';
in
{
  inherit plugin llamaRuntimeLibs;
  default = tix;
  sessionTools = [ tix ];
}
