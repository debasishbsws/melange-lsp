# melange-lsp Implementation Plan

Language Server Protocol implementation for melange YAML configuration files.

## Implementation Phases

### Phase 1: LSP Skeleton + Basic Parsing

**Goal:** Working LSP server that connects to editors.

**Deliverables:**
- LSP server responds to initialize/shutdown
- Can open melange YAML files
- Provides document symbols (outline view)
- Basic YAML parsing

**Tasks:**
- Implement JSON-RPC protocol handler
- Handle LSP lifecycle (initialize, shutdown, exit)
- Implement textDocument/didOpen, didChange, didClose
- Parse YAML and build symbol tree
- Test with editor

### Phase 2: Schema Validation + Basic Completion

**Goal:** Validate configs and provide autocompletion.

**Deliverables:**
- Diagnostics for invalid configs
- Required field validation
- Autocomplete top-level keys and package fields

**Tasks:**
- Define/import melange config schema
- Implement textDocument/publishDiagnostics
- Implement textDocument/completion
- Provide completions based on YAML context

### Phase 3: Pipeline Discovery + Documentation

**Goal:** Support for built-in pipelines.

**Deliverables:**
- Autocomplete pipeline names in `uses:` directives
- Hover shows pipeline description and parameters
- Validate pipeline inputs

**Tasks:**
- Parse pipeline YAML files (from melange source or embedded)
- Build pipeline registry
- Implement textDocument/hover
- Validate pipeline parameters

### Phase 4: Template Variable Support

**Goal:** Full support for `${{...}}` template variables.

**Deliverables:**
- Autocomplete template variables
- Validate variable references
- Syntax highlighting for templates

**Tasks:**
- Parse `${{...}}` syntax
- Build variable context
- Provide completion inside templates
- Validate variable paths

### Phase 5: Advanced Features

**Goal:** Polish and advanced IDE features.

**Deliverables:**
- Go-to-definition
- Find references
- Code actions
- Formatting
- Snippets

**Tasks:**
- Implement navigation features
- Add quick fixes
- Document formatting support

## Next Steps

### Immediate (Phase 1)

1. Set up LSP protocol handler
2. Implement basic lifecycle (initialize/shutdown)
3. Add YAML parsing
4. Provide document symbols
5. Test with editor

### Short Term (Phases 2-3)

1. Schema validation
2. Basic completion
3. Pipeline discovery
4. Hover documentation

### Long Term (Phases 4-5)

1. Template variable support
2. Advanced IDE features
3. Performance optimization
