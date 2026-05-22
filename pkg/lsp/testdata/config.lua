-- Minimal test config: load dang.nvim from the editors/nvim submodule
vim.opt.runtimepath:prepend('../../editors/nvim')

-- Source ftdetect since the plugin was added to rtp after startup
vim.cmd('runtime! ftdetect/*.lua')

-- Load and configure the plugin
require('dang').setup()

-- Keymaps used by tests:
--   K    -> vim.lsp.buf.hover()    (Neovim default)
--   grn  -> vim.lsp.buf.rename()   (Neovim default)
--   gd   -> vim.lsp.buf.definition() (bound here; Neovim 0.12 has no default)
-- Omnicompletion (C-x C-o) uses omnifunc set automatically by LSP.
vim.keymap.set('n', 'gd', function() vim.lsp.buf.definition() end)

vim.lsp.log.set_level('trace')
