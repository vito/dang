local function read_file(path)
  local f = assert(io.open(path, 'rb'))
  local content = f:read('*a')
  f:close()
  return content
end

local function write_file(path, content)
  local f = assert(io.open(path, 'wb'))
  f:write(content)
  f:close()
end

local function split_lines(source)
  source = source:gsub('\r\n', '\n')

  local lines = {}
  local start = 1
  while true do
    local newline = source:find('\n', start, true)
    if not newline then
      lines[#lines + 1] = source:sub(start)
      break
    end
    lines[#lines + 1] = source:sub(start, newline - 1)
    start = newline + 1
  end

  if #lines == 0 then
    return { '' }
  end
  return lines
end

local function range6(range)
  if #range == 6 then
    return range
  end

  error('expected Range6, got ' .. vim.inspect(range))
end

local function intersect_ranges(a, b)
  local start_byte = math.max(a[3], b[3])
  local end_byte = math.min(a[6], b[6])
  if start_byte >= end_byte then
    return nil
  end
  return { start_byte, end_byte }
end

local function treesitter_priority()
  if vim.hl and vim.hl.priorities and vim.hl.priorities.treesitter then
    return vim.hl.priorities.treesitter
  end
  if vim.highlight and vim.highlight.priorities and vim.highlight.priorities.treesitter then
    return vim.highlight.priorities.treesitter
  end
  return 100
end

local function capture_priority(metadata, id)
  local priority = metadata and metadata.priority
  if priority == nil and metadata and metadata[id] then
    priority = metadata[id].priority
  end
  return tonumber(priority) or treesitter_priority()
end

local function dump_case(case)
  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_set_current_buf(buf)
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, split_lines(case.source))
  vim.bo[buf].filetype = 'dang'

  vim.treesitter.start(buf, 'dang')
  vim.cmd('redraw')

  local parser = vim.treesitter.get_parser(buf, 'dang')
  parser:parse()

  local spans = {}
  local order = 0

  parser:for_each_tree(function(tstree, ltree)
    local query = vim.treesitter.query.get(ltree:lang(), 'highlights')
    if not query then
      return
    end

    local included_ranges = tstree:included_ranges(true)
    for id, node, metadata in query:iter_captures(tstree:root(), buf, 0, -1) do
      local capture = query.captures[id]
      if capture then
        local range = range6(vim.treesitter.get_range(node, buf, metadata and metadata[id]))
        for _, included_range in ipairs(included_ranges) do
          local intersection = intersect_ranges(range, included_range)
          if intersection then
            order = order + 1
            spans[#spans + 1] = {
              capture = capture,
              lang = ltree:lang(),
              start_byte = intersection[1],
              end_byte = intersection[2],
              priority = capture_priority(metadata, id),
              order = order,
            }
          end
        end
      end
    end
  end)

  vim.treesitter.stop(buf)
  vim.api.nvim_buf_delete(buf, { force = true })

  return {
    id = case.id,
    spans = spans,
  }
end

local function run()
  local input_path = assert(vim.env.DANG_HIGHLIGHT_INPUT, 'DANG_HIGHLIGHT_INPUT is required')
  local output_path = assert(vim.env.DANG_HIGHLIGHT_OUTPUT, 'DANG_HIGHLIGHT_OUTPUT is required')
  local input = vim.json.decode(read_file(input_path))

  vim.opt.runtimepath:prepend(input.nvim_plugin_root)

  require('dang').setup({ lsp = false })

  local ok, err = vim.treesitter.language.add('dang', { path = input.parser_path })
  if not ok then
    error('failed to load Dang parser from ' .. input.parser_path .. ': ' .. tostring(err))
  end

  local cases = {}
  for _, case in ipairs(input.cases) do
    cases[#cases + 1] = dump_case(case)
  end

  write_file(output_path, vim.json.encode({ cases = cases }))
end

local ok, err = xpcall(run, debug.traceback)
if not ok then
  local output_path = vim.env.DANG_HIGHLIGHT_OUTPUT
  if output_path and output_path ~= '' then
    pcall(write_file, output_path, vim.json.encode({ error = err }))
  end
  vim.api.nvim_err_writeln(err)
  vim.cmd('cquit 1')
end

vim.cmd('qa!')
