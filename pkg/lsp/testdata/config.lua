-- Minimal test config: load dang.nvim from the editors/nvim submodule
vim.opt.runtimepath:prepend('../../editors/nvim')

-- Source ftdetect since the plugin was added to rtp after startup
vim.cmd('runtime! ftdetect/*.lua')

-- Load and configure the plugin
require('dang').setup()

-- All keymaps used by tests are Neovim 0.11 defaults:
--   gd   -> vim.lsp.buf.definition()
--   K    -> vim.lsp.buf.hover()
--   grn  -> vim.lsp.buf.rename()
-- Omnicompletion (C-x C-o) uses omnifunc set automatically by LSP.

vim.lsp.log.set_level('trace')
