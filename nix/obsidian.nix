{
  nixosModule = { config, lib, ... }: {
    options.tix.obsidian.vaultRoot = lib.mkOption {
      type = lib.types.str;
      default = "~/Documents/Obsidian";
      description = "Folder holding the Obsidian vaults; the only one the Obsidian flatpak may access.";
    };

    config.services.flatpak.packages = [ "md.obsidian.Obsidian" ];

    config.services.flatpak.overrides.settings."md.obsidian.Obsidian".Context = {
      filesystems = [
        "!home"
        "!/mnt"
        "!/run/media"
        "!/media"
        "!xdg-run/app/com.discordapp.Discord"
        "!xdg-run/gnupg"
        "!~/.local/share/fonts"
        # flatpak resolves xdg-documents/... through user-dirs.dirs and grants nothing without one
        "${config.tix.obsidian.vaultRoot}:create"
      ];
      sockets = [ "wayland" "fallback-x11" "pulseaudio" "!ssh-auth" ];
      persistent = [ "!~/.ssh" ];
    };
  };

  homeModule = { config, lib, pkgs, ... }:
    let
      cfg = config.programs.tix.obsidian;
      inherit (cfg) vaults;
      root = cfg.vaultRoot;

      tasksPin = builtins.fromJSON (builtins.readFile ../assets/obsidian/tasks-plugin.json);
      pluginId = tasksPin.id;
      tasksAsset = name: pkgs.fetchurl {
        url = "https://github.com/${tasksPin.repo}/releases/download/${tasksPin.version}/${name}";
        sha256 = tasksPin.sha256.${name};
      };

      minimalVersion = "9.0.2";
      minimalAsset = name: sha256: pkgs.fetchurl {
        url = "https://github.com/kepano/obsidian-minimal/releases/download/${minimalVersion}/${name}";
        inherit sha256;
      };
      minimalCss = minimalAsset "theme.css" "0pvsgxjr9f98knvfmgm8is37qz6m3d0v1dgag4lcirn6lm7xhx49";
      minimalManifest = minimalAsset "manifest.json" "0dh5d28fin0gq2dnn2gf9xfhbg3j6n9pfkbvsm5awbma0a8r7ji2";

      setIfUnset = pkgs.writeShellScript "obsidian-set-if-unset" ''
        set -eu
        file="$1" key="$2" value="$3"
        current="$(cat "$file" 2>/dev/null || true)"
        [ -n "$current" ] || current='{}'
        if ${lib.getExe pkgs.jq} -e --arg k "$key" '.[$k] == null or .[$k] == ""' <<< "$current" >/dev/null; then
          ${lib.getExe pkgs.jq} --arg k "$key" --argjson v "$value" '.[$k] = $v' <<< "$current" > "$file.tmp"
          mv "$file.tmp" "$file"
        fi
      '';

      registerVaults = pkgs.writeShellScript "obsidian-register-vaults" ''
        set -eu
        if ${lib.getExe pkgs.flatpak} ps --columns=application 2>/dev/null | grep -qx md.obsidian.Obsidian; then
          echo "obsidian: running, not registering vaults in obsidian.json (quit it and switch again)" >&2
          exit 0
        fi
        root="$1"
        shift
        file="$HOME/.var/app/md.obsidian.Obsidian/config/obsidian/obsidian.json"
        mkdir -p "$(dirname "$file")"
        current="$(cat "$file" 2>/dev/null || true)"
        [ -n "$current" ] || current='{}'
        ids="$(for v in "$@"; do
          printf '%s %s\n' "$v" "$(printf '%s' "$root/$v" | sha256sum | cut -c1-16)"
        done)"
        ${lib.getExe pkgs.jq} -c --arg root "$root" --arg ids "$ids" --argjson ts "$(date +%s%3N)" '
          .vaults = ((.vaults // {}) | map_values(
            if (.path // "") | test("^/run/user/[0-9]+/doc/")
            then .path = ($root + "/" + (.path | split("/") | last))
            else . end))
          | reduce ($ids | split("\n")[] | select(. != "") | split(" ")) as [$name, $id] (.;
              ($root + "/" + $name) as $path
              | if any(.vaults[]; .path == $path) then .
                else .vaults[$id] = { path: $path, ts: $ts } end)
        ' <<< "$current" > "$file.tmp"
        if cmp -s "$file.tmp" "$file"; then rm "$file.tmp"; else mv "$file.tmp" "$file"; fi
      '';

      seedVaultScript = vault: ''
        vault_dir="${root}/${vault}"
        plugin_dir="$vault_dir/.obsidian/plugins/${pluginId}"
        if [ ! -e "$plugin_dir/main.js" ]; then
          run mkdir -p "$plugin_dir"
          ${lib.concatMapStringsSep "\n          " (n: ''run install -m 0644 ${tasksAsset n} "$plugin_dir/${n}"'') (lib.attrNames tasksPin.sha256)}
        fi

        run install -m 0644 ${../assets/obsidian/tasks-settings.json} "$plugin_dir/data.json"

        community_plugins="$vault_dir/.obsidian/community-plugins.json"
        if [ ! -e "$community_plugins" ]; then
          run mkdir -p "$vault_dir/.obsidian"
          run bash -c 'echo ${lib.escapeShellArg (builtins.toJSON [ pluginId ])} > "$1"' _ "$community_plugins"
        fi

        theme_dir="$vault_dir/.obsidian/themes/Minimal"
        if [ ! -e "$theme_dir/theme.css" ]; then
          run mkdir -p "$theme_dir"
          run install -m 0644 ${minimalCss} "$theme_dir/theme.css"
          run install -m 0644 ${minimalManifest} "$theme_dir/manifest.json"
        fi
        run ${setIfUnset} "$vault_dir/.obsidian/appearance.json" cssTheme '"Minimal"'
        run ${setIfUnset} "$vault_dir/.obsidian/app.json" readableLineLength false
      '';
    in {
      options.programs.tix.obsidian = {
        vaults = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          default = [ ];
          example = [ "work" "personal" ];
          description = "Vaults (folder names under vaultRoot) to seed with the Tasks plugin, its settings and the Minimal theme, and register with Obsidian.";
        };
        vaultRoot = lib.mkOption {
          type = lib.types.str;
          default = "${config.home.homeDirectory}/Documents/Obsidian";
          description = "Folder holding the vaults (OBSIDIAN_ROOT for the tix commands).";
        };
      };

      config = lib.mkIf (vaults != [ ]) {
        home.activation.obsidianVaults =
          lib.hm.dag.entryAfter [ "writeBoundary" ]
            (lib.concatMapStringsSep "\n" seedVaultScript vaults + ''

              run ${registerVaults} ${lib.escapeShellArg root} ${lib.escapeShellArgs vaults}
            '');
      };
    };
}
