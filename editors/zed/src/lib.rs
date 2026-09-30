//! Zed extension that runs melange-lsp for YAML files.

use zed_extension_api::{self as zed, settings::LspSettings, LanguageServerId, Result};

const BINARY: &str = "melange-lsp";

struct MelangeExtension;

impl zed::Extension for MelangeExtension {
    fn new() -> Self {
        MelangeExtension
    }

    fn language_server_command(
        &mut self,
        server_id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<zed::Command> {
        // An explicit binary in settings wins, then melange-lsp on PATH.
        let settings = LspSettings::for_worktree(server_id.as_ref(), worktree).ok();
        if let Some(binary) = settings.and_then(|s| s.binary) {
            if let Some(path) = binary.path {
                return Ok(zed::Command {
                    command: path,
                    args: binary.arguments.unwrap_or_default(),
                    env: Default::default(),
                });
            }
        }
        let path = worktree.which(BINARY).ok_or_else(|| {
            format!("{BINARY} not found on PATH; install it with `go install ./cmd/melange-lsp` from the melange-lsp repository")
        })?;
        Ok(zed::Command {
            command: path,
            args: vec![],
            env: worktree.shell_env(),
        })
    }

    fn language_server_initialization_options(
        &mut self,
        server_id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<Option<zed::serde_json::Value>> {
        Ok(LspSettings::for_worktree(server_id.as_ref(), worktree)
            .ok()
            .and_then(|s| s.initialization_options))
    }

    fn language_server_workspace_configuration(
        &mut self,
        server_id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<Option<zed::serde_json::Value>> {
        Ok(LspSettings::for_worktree(server_id.as_ref(), worktree)
            .ok()
            .and_then(|s| s.settings))
    }
}

zed::register_extension!(MelangeExtension);
