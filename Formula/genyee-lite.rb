class GenyeeLite < Formula
  desc "Knowledge base management system — single-binary Lite edition"
  homepage "https://genyee.io"
  version "0.3.6-test"
  license "LicenseRef-GENYEE-Commercial"

  on_macos do
    on_arm do
      url "https://github.com/genyee/genyee/releases/download/v#{version}/GENYEE-lite_v#{version}_darwin_arm64.tar.gz"
      sha256 "1da2d4eef99e5cf8aa7a58501baa059e9e20482e1bd65a36a82321a89926c104"
    end
    on_intel do
      url "https://github.com/genyee/genyee/releases/download/v#{version}/GENYEE-lite_v#{version}_darwin_amd64.tar.gz"
      sha256 "c187e16ac7671a615f012c82ebd89786e11fcf67cccc773eff175e4bdf7c9c06"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/genyee/genyee/releases/download/v#{version}/GENYEE-lite_v#{version}_linux_arm64.tar.gz"
      sha256 "bc4e184da005b60d1e8c037a61c58e643ebdc9bf14470fae6cd6227f52f02f1c"
    end
    on_intel do
      url "https://github.com/genyee/genyee/releases/download/v#{version}/GENYEE-lite_v#{version}_linux_amd64.tar.gz"
      sha256 "cb34c50fb5b05555fca16084ffc7710524ff78badb3b1b82474eb89d21545d6e"
    end
  end

  def install
    libexec.install "GENYEE-lite"
    pkgshare.install "web" if File.directory?("web")
    pkgshare.install "config" if File.directory?("config")
    pkgshare.install ".env.lite.example"
    doc.install "README.md"
    pkgshare.install "migrations" if File.directory?("migrations")

    (bin/"genyee-lite").write <<~SH
      #!/bin/bash
      CONFIG_DIR="${GENYEE_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/genyee}"
      DATA_DIR="${GENYEE_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/genyee}"

      mkdir -p "$DATA_DIR/files" "$CONFIG_DIR/config" 2>/dev/null

      if [ ! -f "$CONFIG_DIR/config/config.yaml" ]; then
        cp -R "#{pkgshare}/config/." "$CONFIG_DIR/config/"
      fi

      if [ ! -d "$CONFIG_DIR/migrations" ] && [ -d "#{pkgshare}/migrations" ]; then
        ln -sf "#{pkgshare}/migrations" "$CONFIG_DIR/migrations"
      fi

      if [ ! -f "$CONFIG_DIR/.env.lite" ]; then
        (
          umask 077
          cp "#{pkgshare}/.env.lite.example" "$CONFIG_DIR/.env.lite"
          chmod 0600 "$CONFIG_DIR/.env.lite"
          ESCAPED_DB_PATH=$(printf '%s\n' "$DATA_DIR/genyee.db" | sed -e 's/[\\\\&|]/\\\\&/g')
          ESCAPED_STORAGE_DIR=$(printf '%s\n' "$DATA_DIR/files" | sed -e 's/[\\\\&|]/\\\\&/g')
          sed "s|DB_PATH=.*|DB_PATH=$ESCAPED_DB_PATH|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
          sed "s|LOCAL_STORAGE_BASE_DIR=.*|LOCAL_STORAGE_BASE_DIR=$ESCAPED_STORAGE_DIR|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
          chmod 0600 "$CONFIG_DIR/.env.lite"
        )
        echo ""
        echo "已创建配置文件: $CONFIG_DIR/.env.lite"
        echo "请根据需要编辑（如修改 LLM 地址、安全密钥等）。"
        echo ""
      fi

      CR=$(printf "\\r")
      while IFS= read -r line || [ -n "$line" ]; do
        line="${line%"$CR"}"
        case "$line" in
          ""|"#"*|[[:space:]]*"#"*) continue ;;
          *=*)
            key="${line%%=*}"
            val="${line#*=}"
            key="${key#"${key%%[![:space:]]*}"}"
            case "$key" in
              "export "[[:space:]]*|"export	"*)
                key="${key#export}"
                key="${key#"${key%%[![:space:]]*}"}"
                ;;
            esac
            key="${key%"${key##*[![:space:]]}"}"
            case "$key" in
              ""|[!A-Za-z_]*|*[!A-Za-z0-9_]*) continue ;;
            esac
            case "$val" in
              '"'*'"'|"'"*"'") val="${val#?}"; val="${val%?}" ;;
            esac
            export "$key=$val"
            ;;
        esac
      done < "$CONFIG_DIR/.env.lite"

      export DB_PATH="${DB_PATH:-$DATA_DIR/genyee.db}"
      export LOCAL_STORAGE_BASE_DIR="${LOCAL_STORAGE_BASE_DIR:-$DATA_DIR/files}"
      export GENYEE_WEB_DIR="${GENYEE_WEB_DIR:-#{pkgshare}/web}"

      cd "$CONFIG_DIR"
      exec "#{libexec}/GENYEE-lite" "$@"
    SH
  end

  def post_install
    (var/"genyee").mkpath
    (var/"log").mkpath
  end

  service do
    run [bin/"genyee-lite"]
    keep_alive true
    working_dir var/"genyee"
    log_path var/"log/genyee-lite.log"
    error_log_path var/"log/genyee-lite.log"
  end

  def caveats
    <<~EOS
      前台运行:
        genyee-lite

      后台服务（推荐）:
        brew services start genyee-lite   # 启动并开机自启
        brew services stop genyee-lite    # 停止
        brew services restart genyee-lite # 重启
        brew services info genyee-lite    # 查看状态

      日志:
        #{var}/log/genyee-lite.log

      首次运行会自动创建配置文件:
        ~/.config/genyee/.env.lite

      数据存储在:
        ~/.local/share/genyee/

      如需修改配置（LLM 服务地址、安全密钥等）:
        $EDITOR ~/.config/genyee/.env.lite
        brew services restart genyee-lite
    EOS
  end

  test do
    assert_predicate bin/"genyee-lite", :executable?
  end
end
