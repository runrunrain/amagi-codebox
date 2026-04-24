<template>
  <div class="provider-detail">
    <div class="breadcrumb">
      <a href="#" @click.prevent="router.push('/providers')" class="back-link">服务提供商</a>
      <span class="separator">/</span>
      <span class="current">{{ name }}</span>
    </div>

    <div class="card provider-info">
      <div class="card-header">
        <h2>基本信息</h2>
        <button class="btn primary" @click="handleSaveProvider" :disabled="loading">
          {{ loading ? '保存中...' : '保存修改' }}
        </button>
      </div>
      <div class="card-body" v-if="provider">
        <div class="form-grid">
          <div class="form-group">
            <label>基础 URL (Base URL)</label>
            <div class="url-input-group">
              <div class="url-autocomplete-wrapper">
                <el-autocomplete
                  v-model="provider.base_url"
                  :fetch-suggestions="queryUrlHistory"
                  placeholder="输入或选择 Base URL"
                  :loading="urlHistoryLoading"
                  class="url-autocomplete"
                  @select="onBaseUrlSelect"
                  @blur="onBaseUrlBlur"
                  :debounce="0"
                  popper-class="url-history-dropdown"
                  clearable
                >
                  <template #default="{ item }">
                    <div class="url-history-item">
                      <span class="url-text">{{ item.value }}</span>
                      <el-icon class="url-delete-btn" @click.stop="handleRemoveUrl(item.value)">
                        <Close />
                      </el-icon>
                    </div>
                  </template>
                  <template #empty>
                    <div class="url-empty-state">
                      <span>暂无历史 URL</span>
                    </div>
                  </template>
                </el-autocomplete>
              </div>
              <button class="btn-secondary url-save-btn" @click="handleSaveBaseUrl" :disabled="loading || !provider.base_url?.trim()">
                保存
              </button>
            </div>
          </div>
          <div class="form-group">
            <label>默认模型</label>
            <input type="text" v-model="provider.default_model" class="input-field" />
          </div>
          <div class="form-group">
            <label>认证密钥类型</label>
            <select v-model="provider.auth_key" class="input-field">
              <option value="ANTHROPIC_API_KEY" v-if="!provider.type || provider.type === 'anthropic'">ANTHROPIC_API_KEY</option>
              <option value="ANTHROPIC_AUTH_TOKEN" v-if="!provider.type || provider.type === 'anthropic'">ANTHROPIC_AUTH_TOKEN</option>
              <option value="OAUTH" v-if="!provider.type || provider.type === 'anthropic'">OAUTH（订阅认证）</option>
              <option value="OPENAI_API_KEY" v-if="provider.type === 'openai'">OPENAI_API_KEY</option>
            </select>
          </div>
        </div>
      </div>
    </div>

    <div class="card api-key-card" v-if="provider">
      <div class="card-header">
        <div class="card-title-group">
          <h2>API 密钥</h2>
          <span :class="['status-badge', apiKeyState.hasKey ? 'status-configured' : 'status-unconfigured']">
            {{ apiKeyState.hasKey ? '已配置' : '未配置' }}
          </span>
        </div>
      </div>
      <div class="card-body">
        <div v-if="provider.auth_key === 'OAUTH'" class="oauth-notice">
          此提供商使用 OAuth 认证，无需配置 API 密钥
        </div>

        <template v-else>
          <div class="key-display" v-if="apiKeyState.hasKey && !apiKeyState.isEditing">
            <span class="key-text">{{ maskedApiKey }}</span>
            <div class="key-actions">
              <button class="btn secondary btn-small" @click="toggleKeyVisibility" :disabled="loading">
                {{ apiKeyState.isVisible ? '隐藏' : '显示' }}
              </button>
              <button class="btn secondary btn-small" @click="startKeyEdit" :disabled="loading">
                编辑
              </button>
              <template v-if="apiKeyState.isConfirmingDelete">
                <span class="confirm-text">确认删除？</span>
                <button class="btn danger-outline btn-small" @click="deleteApiKey" :disabled="loading">
                  {{ loading ? '处理中...' : '确认' }}
                </button>
                <button class="btn secondary btn-small" @click="apiKeyState.isConfirmingDelete = false" :disabled="loading">
                  取消
                </button>
              </template>
              <button v-else class="btn danger-outline btn-small" @click="apiKeyState.isConfirmingDelete = true" :disabled="loading">
                删除
              </button>
            </div>
          </div>

          <div class="form-group api-key-input-group" v-else>
            <label>{{ apiKeyState.isEditing ? 'API 密钥' : '设置 API 密钥' }}</label>
            <div class="key-input-row">
              <input
                :type="apiKeyState.inputVisible ? 'text' : 'password'"
                v-model="apiKeyState.inputValue"
                class="input-field"
                :placeholder="apiKeyState.isEditing ? '输入新的 API 密钥' : '输入 API 密钥'"
              />
              <button class="btn secondary btn-small" @click="apiKeyState.inputVisible = !apiKeyState.inputVisible" :disabled="loading">
                {{ apiKeyState.inputVisible ? '隐藏' : '明文' }}
              </button>
              <button class="btn primary" @click="saveApiKey" :disabled="!apiKeyState.inputValue || loading">
                {{ loading ? '保存中...' : '保存' }}
              </button>
              <button v-if="apiKeyState.isEditing" class="btn secondary" @click="cancelKeyEdit" :disabled="loading">
                取消
              </button>
            </div>
          </div>
        </template>
      </div>
    </div>

    <div class="presets-section">
      <div class="section-header">
        <div>
          <h2>预设配置 <span class="legacy-badge">旧体系</span></h2>
          <div class="migration-hint-box migration-hint-box-prominent">
            <p class="migration-hint-strong">预设已迁移至"设置 &gt; 终端预设"体系管理，此页面仅保留兼容查看入口。</p>
            <p class="migration-hint">新增或编辑预设请前往新入口，以获得更完整的字段支持和跨终端复用能力。</p>
            <div class="migration-cta-row">
              <button class="btn primary btn-small migration-cta-btn" @click="router.push('/settings')">前往 设置 &gt; 终端预设</button>
            </div>
          </div>
        </div>
        <div class="section-header-actions">
          <button class="btn secondary" @click="openJsonEditor">JSON 编辑</button>
          <button class="btn-text-link" @click="openAddPresetDialog">+ 添加预设(旧)</button>
        </div>
      </div>

      <div class="presets-list">
        <div class="card preset-card" v-for="(preset, presetName) in provider?.presets" :key="presetName">
          <div class="preset-header">
            <h3 class="preset-name">{{ presetName }}</h3>
            <div class="preset-actions">
              <button class="btn-icon" @click="openEditPresetDialog(String(presetName), preset)" title="编辑">
                <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path>
                </svg>
              </button>
              <button class="btn-icon danger" @click="handleDeletePreset(String(presetName))" title="删除">
                <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="3 6 5 6 21 6"></polyline>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                </svg>
              </button>
            </div>
          </div>
          <div class="preset-body">
            <div class="info-row">
              <span class="label">模型:</span>
              <span class="value">{{ preset.model || '继承默认' }}</span>
            </div>
            <div class="info-row" v-if="preset.target">
              <span class="label">目标:</span>
              <span class="value">{{ preset.target === 'opencode' ? 'OpenCode' : preset.target === 'codex' ? 'Codex' : preset.target }}</span>
            </div>
            <div class="info-row" v-if="preset.opencode_config">
              <span class="label">OC Config:</span>
              <span class="value oc-config-summary">{{ ocConfigSummary(preset.opencode_config) }}</span>
            </div>
            <div class="params-summary">
              <span class="param-badge" v-if="preset.parameters?.temperature !== undefined">Temp: {{ preset.parameters.temperature }}</span>
              <span class="param-badge" v-if="preset.parameters?.top_p !== undefined">Top P: {{ preset.parameters.top_p }}</span>
              <span class="param-badge" v-if="preset.parameters?.max_tokens !== undefined">Max Tokens: {{ preset.parameters.max_tokens }}</span>
              <span class="param-badge" v-if="preset.parameters?.max_context_length !== undefined">CtxLen: {{ preset.parameters.max_context_length }}</span>
              <span class="param-badge" v-if="preset.parameters?.context_window?.model_context_window" title="Model Context Window">
                Window: {{ formatContextSize(preset.parameters.context_window.model_context_window) }}
              </span>
              <span class="param-badge" v-if="preset.parameters?.context_window?.model_auto_compact_token_limit" title="Auto Compact Limit">
                Compact@: {{ formatContextSize(preset.parameters.context_window.model_auto_compact_token_limit) }}
              </span>
              <span class="param-badge" v-if="preset.parameters?.stream">Stream</span>
              <span class="param-badge" v-if="preset.parameters?.thinking?.type === 'enabled'">Thinking ({{ preset.parameters.thinking.budgetTokens || 'auto' }})</span>
            </div>
          </div>
        </div>
        
        <div v-if="!provider?.presets || Object.keys(provider.presets).length === 0" class="empty-state">
          <p class="muted">暂无预设配置</p>
        </div>
      </div>
    </div>

    <!-- JSON Editor Dialog -->
    <div class="dialog-overlay" v-if="showJsonEditor" @click.self="showJsonEditor = false">
      <div class="dialog card json-editor-dialog">
        <h2>JSON 编辑 - {{ providerName }}</h2>
        <div class="json-editor-body">
          <textarea
            v-model="jsonEditorContent"
            class="json-textarea"
            spellcheck="false"
            placeholder="加载中..."
          ></textarea>
          <div class="json-status" :class="{ error: !!jsonError, success: !jsonError && jsonEditorContent }">
            <span v-if="!jsonEditorContent"></span>
            <span v-else-if="jsonError">语法错误: {{ jsonError }}</span>
            <span v-else>JSON 语法正确</span>
          </div>
        </div>
        <div class="dialog-actions">
          <button class="btn secondary" @click="showJsonEditor = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="handleSaveJson" :disabled="!!jsonError || !jsonEditorContent || loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Preset Dialog -->
    <div class="dialog-overlay" v-if="showPresetDialog" @click.self="showPresetDialog = false">
      <div class="dialog card preset-dialog">
        <h2>{{ isEditingPreset ? '编辑预设' : '添加预设' }}</h2>
        
        <div class="dialog-scroll-area">
          <div class="form-group" v-if="!isEditingPreset">
            <label>预设名称 (唯一标识)</label>
            <input type="text" v-model="editingPresetName" class="input-field" placeholder="例如: default, coding, writing" />
          </div>
          
          <div class="form-group">
            <label>模型 (留空则使用提供商默认模型)</label>
            <input type="text" v-model="editingPreset.model" class="input-field" placeholder="例如: claude-3-7-sonnet-20250219" />
          </div>

          <h3 class="section-subtitle">参数配置</h3>
          
          <div class="form-grid-2">
            <div class="form-group">
              <label>Temperature (温度)</label>
              <input type="number" v-model.number="editingPreset.parameters.temperature" class="input-field" step="0.1" min="0" max="1" placeholder="默认" />
            </div>
            <div class="form-group">
              <label>Top P</label>
              <input type="number" v-model.number="editingPreset.parameters.top_p" class="input-field" step="0.1" min="0" max="1" placeholder="默认" />
            </div>
            <div class="form-group">
              <label>Max Tokens (最大输出Token数)</label>
              <input type="number" v-model.number="editingPreset.parameters.max_tokens" class="input-field" step="1" min="1" placeholder="默认" />
            </div>
            <div class="form-group">
              <label>Context Window (上下文窗口大小)</label>
              <input type="number" v-model.number="editingPreset.parameters.max_context_length" class="input-field" step="1" min="1" placeholder="默认" />
              <p class="field-hint">设置 Claude Code 的上下文窗口容量（token 数），用于控制自动压缩的触发时机。</p>
            </div>
          </div>

          <h3 class="section-subtitle">上下文窗口高级配置 (Codex CLI 风格)</h3>
          <div class="context-window-config">
            <div class="form-group">
              <label>Model Context Window (上下文窗口大小)</label>
              <div class="input-with-presets">
                <input type="number" v-model.number="contextWindowModel" class="input-field" step="1" min="1" placeholder="默认" />
                <div class="preset-buttons">
                  <button type="button" class="btn-xs" @click="setContextWindow(200000)" title="200K">200K</button>
                  <button type="button" class="btn-xs" @click="setContextWindow(500000)" title="500K">500K</button>
                  <button type="button" class="btn-xs" @click="setContextWindow(1047576)" title="1M">1M</button>
                  <button type="button" class="btn-xs" @click="setContextWindow(2097152)" title="2M">2M</button>
                </div>
              </div>
              <p class="field-hint">设置模型的上下文窗口大小（如 GPT-5.4 的 1047576 = 1M token）</p>
            </div>
            <div class="form-group">
              <label>Auto Compact Token Limit (自动压缩阈值)</label>
              <div class="input-with-presets">
                <input type="number" v-model.number="contextWindowCompact" class="input-field" step="1" min="1" placeholder="默认" />
                <div class="preset-buttons">
                  <button type="button" class="btn-xs" @click="setCompactLimit(100000)" title="100K">100K</button>
                  <button type="button" class="btn-xs" @click="setCompactLimit(105197)" title="105K (GPT-5.4 推荐)">105K</button>
                  <button type="button" class="btn-xs" @click="setCompactLimit(180000)" title="180K">180K</button>
                  <button type="button" class="btn-xs" @click="setCompactLimit(200000)" title="200K">200K</button>
                </div>
              </div>
              <p class="field-hint">当历史上下文达到此 token 数时触发自动压缩（通常设置为窗口大小的 10%-20%）</p>
            </div>
          </div>

          <div class="checkbox-group-inline">
            <label class="checkbox-label">
              <input type="checkbox" v-model="editingPreset.parameters.do_sample" />
              <span class="checkbox-text">Do Sample</span>
            </label>
            <label class="checkbox-label">
              <input type="checkbox" v-model="editingPreset.parameters.stream" />
              <span class="checkbox-text">Stream (流式输出)</span>
            </label>
          </div>

          <h3 class="section-subtitle">思考配置 (Thinking)</h3>
          <div class="form-group">
            <label>思考模式</label>
            <select v-model="thinkingType" class="input-field">
              <option value="">默认 (不配置)</option>
              <option value="disabled">禁用 (Disabled)</option>
              <option value="enabled">启用 (Enabled)</option>
            </select>
          </div>
          
          <div class="form-group" v-if="thinkingType === 'enabled'">
            <label>思考预算 Tokens (Budget Tokens)</label>
            <input type="number" v-model.number="thinkingBudget" class="input-field" step="1" min="1024" placeholder="例如: 16384" />
          </div>

          <h3 class="section-subtitle">目标平台与高级配置</h3>
          <div class="form-group">
            <label>目标平台 (Target)</label>
            <select v-model="editingPresetTarget" class="input-field">
              <option value="">Codex (默认)</option>
              <option value="opencode">OpenCode</option>
            </select>
            <p class="field-hint">指定此预设用于哪个 CLI 工具。Codex 菜单仅显示 Codex 预设，OpenCode 菜单仅显示 OpenCode 预设。</p>
          </div>

          <!-- OpenCode Structured GUI -->
          <div v-if="editingPresetTarget === 'opencode'" class="opencode-gui-section">
            <div class="opencode-gui-header">
              <span class="opencode-gui-title">OpenCode 配置</span>
              <span class="opencode-gui-hint">结构化编辑常用配置，高级 JSON 面板保真所有字段</span>
            </div>

            <!-- Provider / Model -->
            <div class="oc-collapsible" :class="{ expanded: ocExpandedSections.provider }">
              <div class="oc-collapsible-header" @click="toggleOcSection('provider')">
                <span class="oc-collapse-icon">{{ ocExpandedSections.provider ? '&#9660;' : '&#9654;' }}</span>
                <span>Provider / Model</span>
              </div>
              <div class="oc-collapsible-body" v-if="ocExpandedSections.provider">
                <div class="form-group">
                  <label>Model (provider/model 格式)</label>
                  <input type="text" v-model="ocGuiState.model" class="input-field" placeholder="例如: anthropic/claude-opus-4-6, openai/gpt-5.4" @input="ocGuiToRaw" />
                  <p class="field-hint">OpenCode 的 model 字段，格式为 provider-id/model-name</p>
                </div>
                <div class="form-group">
                  <label>Provider 配置</label>
                  <p class="field-hint">Provider 详细配置请在下方"高级 JSON"面板中编辑</p>
                </div>
              </div>
            </div>

            <!-- MCP Servers -->
            <div class="oc-collapsible" :class="{ expanded: ocExpandedSections.mcp }">
              <div class="oc-collapsible-header" @click="toggleOcSection('mcp')">
                <span class="oc-collapse-icon">{{ ocExpandedSections.mcp ? '&#9660;' : '&#9654;' }}</span>
                <span>MCP Servers <span class="oc-count-badge" v-if="ocGuiState.mcpServers.length">{{ ocGuiState.mcpServers.length }}</span></span>
              </div>
              <div class="oc-collapsible-body" v-if="ocExpandedSections.mcp">
                <div v-for="(mcp, idx) in ocGuiState.mcpServers" :key="idx" class="oc-list-item">
                  <div class="oc-list-item-header">
                    <span class="oc-list-item-name">{{ mcp.name || '(unnamed)' }}</span>
                    <button class="btn-icon danger" @click="removeOcMcp(idx)" title="删除">
                      <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                    </button>
                  </div>
                  <div class="form-grid-2">
                    <div class="form-group">
                      <label>名称</label>
                      <input type="text" v-model="mcp.name" class="input-field" placeholder="my-mcp-server" @input="ocGuiToRaw" />
                    </div>
                    <div class="form-group">
                      <label>类型</label>
                      <select v-model="mcp.type" class="input-field" @change="ocGuiToRaw">
                        <option value="remote">Remote</option>
                        <option value="local">Local</option>
                      </select>
                    </div>
                  </div>
                  <div class="form-group" v-if="mcp.type === 'remote'">
                    <label>URL</label>
                    <input type="text" v-model="mcp.url" class="input-field" placeholder="https://..." @input="ocGuiToRaw" />
                  </div>
                  <div class="form-group" v-if="mcp.type === 'local'">
                    <label>Command (命令参数)</label>
                    <div v-for="(arg, aidx) in mcp.commandArgs" :key="aidx" class="oc-kv-row">
                      <input type="text" v-model="mcp.commandArgs[aidx]" class="input-field" :placeholder="aidx === 0 ? '可执行文件 (如 uvx)' : '参数'" @input="ocGuiToRaw" />
                      <button class="btn-icon danger" @click="mcp.commandArgs.splice(aidx, 1); ocGuiToRaw()" title="删除">
                        <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                      </button>
                    </div>
                    <button class="btn secondary btn-small" @click="mcp.commandArgs.push(''); ocGuiToRaw()">+ 添加参数</button>
                  </div>
                  <div class="form-group">
                    <label>Headers</label>
                    <div v-for="(h, hidx) in mcp.headers" :key="hidx" class="oc-kv-row">
                      <input type="text" v-model="h.key" class="input-field oc-kv-key" placeholder="Header 名称" @input="ocGuiToRaw" />
                      <input type="text" v-model="h.value" class="input-field oc-kv-value" placeholder="值" @input="ocGuiToRaw" />
                      <button class="btn-icon danger" @click="mcp.headers.splice(hidx, 1); ocGuiToRaw()" title="删除">
                        <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                      </button>
                    </div>
                    <button class="btn secondary btn-small" @click="mcp.headers.push({key:'', value:''}); ocGuiToRaw()">+ 添加 Header</button>
                  </div>
                  <div class="form-group">
                    <label>Environment</label>
                    <div v-for="(e, eidx) in mcp.environment" :key="eidx" class="oc-kv-row">
                      <input type="text" v-model="e.key" class="input-field oc-kv-key" placeholder="变量名" @input="ocGuiToRaw" />
                      <input type="text" v-model="e.value" class="input-field oc-kv-value" placeholder="值 (如 {env:MY_KEY})" @input="ocGuiToRaw" />
                      <button class="btn-icon danger" @click="mcp.environment.splice(eidx, 1); ocGuiToRaw()" title="删除">
                        <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                      </button>
                    </div>
                    <button class="btn secondary btn-small" @click="mcp.environment.push({key:'', value:''}); ocGuiToRaw()">+ 添加环境变量</button>
                  </div>
                  <div class="checkbox-group-inline">
                    <label class="checkbox-label">
                      <input type="checkbox" v-model="mcp.oauth" @change="ocGuiToRaw" />
                      <span class="checkbox-text">OAuth</span>
                    </label>
                  </div>
                </div>
                <button class="btn secondary btn-small" @click="addOcMcp">+ 添加 MCP Server</button>
              </div>
            </div>

            <!-- Agents -->
            <div class="oc-collapsible" :class="{ expanded: ocExpandedSections.agent }">
              <div class="oc-collapsible-header" @click="toggleOcSection('agent')">
                <span class="oc-collapse-icon">{{ ocExpandedSections.agent ? '&#9660;' : '&#9654;' }}</span>
                <span>Agents <span class="oc-count-badge" v-if="ocGuiState.agents.length">{{ ocGuiState.agents.length }}</span></span>
              </div>
              <div class="oc-collapsible-body" v-if="ocExpandedSections.agent">
                <div v-for="(agent, idx) in ocGuiState.agents" :key="idx" class="oc-list-item">
                  <div class="oc-list-item-header">
                    <span class="oc-list-item-name" :style="{ color: agent.color || '#e0e6ed' }">{{ agent.name || '(unnamed)' }}</span>
                    <button class="btn-icon danger" @click="removeOcAgent(idx)" title="删除">
                      <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                    </button>
                  </div>
                  <div class="form-grid-2">
                    <div class="form-group">
                      <label>名称</label>
                      <input type="text" v-model="agent.name" class="input-field" placeholder="my-agent" @input="ocGuiToRaw" />
                    </div>
                    <div class="form-group">
                      <label>模式</label>
                      <select v-model="agent.mode" class="input-field" @change="ocGuiToRaw">
                        <option value="primary">Primary</option>
                        <option value="subagent">Subagent</option>
                      </select>
                    </div>
                  </div>
                  <div class="form-grid-2">
                    <div class="form-group">
                      <label>Model</label>
                      <input type="text" v-model="agent.model" class="input-field" placeholder="provider/model" @input="ocGuiToRaw" />
                    </div>
                    <div class="form-group">
                      <label>Color (十六进制)</label>
                      <input type="text" v-model="agent.color" class="input-field" placeholder="#FF69B4" @input="ocGuiToRaw" />
                    </div>
                  </div>
                  <div class="form-group">
                    <label>描述</label>
                    <input type="text" v-model="agent.description" class="input-field" placeholder="Agent 的简短描述" @input="ocGuiToRaw" />
                  </div>
                  <div class="form-group">
                    <label>Prompt (系统指令)</label>
                    <textarea v-model="agent.prompt" class="input-field" rows="3" placeholder="Agent 的系统提示词" @input="ocGuiToRaw"></textarea>
                  </div>
                  <div class="form-group">
                    <label>Tools 权限</label>
                    <div v-for="(tool, tidx) in agent.tools" :key="tidx" class="oc-kv-row">
                      <input type="text" v-model="tool.name" class="input-field oc-kv-key" placeholder="tool 名称 (如 webfetch)" @input="ocGuiToRaw" />
                      <div class="select-wrapper" style="width: 120px; flex: none;">
                        <select v-model="tool.enabled" class="input-field" @change="ocGuiToRaw">
                          <option :value="true">允许</option>
                          <option :value="false">禁用</option>
                        </select>
                      </div>
                      <button class="btn-icon danger" @click="agent.tools.splice(tidx, 1); ocGuiToRaw()" title="删除">
                        <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                      </button>
                    </div>
                    <button class="btn secondary btn-small" @click="agent.tools.push({name: '', enabled: true}); ocGuiToRaw()">+ 添加 Tool</button>
                  </div>
                </div>
                <button class="btn secondary btn-small" @click="addOcAgent">+ 添加 Agent</button>
              </div>
            </div>

            <!-- Permissions -->
            <div class="oc-collapsible" :class="{ expanded: ocExpandedSections.permission }">
              <div class="oc-collapsible-header" @click="toggleOcSection('permission')">
                <span class="oc-collapse-icon">{{ ocExpandedSections.permission ? '&#9660;' : '&#9654;' }}</span>
                <span>Permissions <span class="oc-count-badge" v-if="ocGuiState.permissions.length">{{ ocGuiState.permissions.length }}</span></span>
              </div>
              <div class="oc-collapsible-body" v-if="ocExpandedSections.permission">
                <div v-for="(perm, idx) in ocGuiState.permissions" :key="idx" class="oc-kv-row">
                  <input type="text" v-model="perm.key" class="input-field oc-kv-key" placeholder="tool 名称" @input="ocGuiToRaw" />
                  <select v-model="perm.value" class="input-field oc-kv-value" @change="ocGuiToRaw">
                    <option value="allow">Allow</option>
                    <option value="deny">Deny</option>
                    <option value="ask">Ask</option>
                  </select>
                  <button class="btn-icon danger" @click="removeOcPermission(idx)" title="删除">
                    <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                  </button>
                </div>
                <button class="btn secondary btn-small" @click="addOcPermission">+ 添加权限</button>
              </div>
            </div>

            <!-- Instructions -->
            <div class="oc-collapsible" :class="{ expanded: ocExpandedSections.instructions }">
              <div class="oc-collapsible-header" @click="toggleOcSection('instructions')">
                <span class="oc-collapse-icon">{{ ocExpandedSections.instructions ? '&#9660;' : '&#9654;' }}</span>
                <span>Instructions <span class="oc-count-badge" v-if="ocGuiState.instructions.length">{{ ocGuiState.instructions.length }}</span></span>
              </div>
              <div class="oc-collapsible-body" v-if="ocExpandedSections.instructions">
                <div v-for="(instr, idx) in ocGuiState.instructions" :key="idx" class="oc-kv-row">
                  <input type="text" v-model="ocGuiState.instructions[idx]" class="input-field" placeholder="resources/path/to/file.md" @input="ocGuiToRaw" />
                  <button class="btn-icon danger" @click="removeOcInstruction(idx)" title="删除">
                    <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                  </button>
                </div>
                <button class="btn secondary btn-small" @click="addOcInstruction">+ 添加 Instruction</button>
              </div>
            </div>

            <!-- Plugins -->
            <div class="oc-collapsible" :class="{ expanded: ocExpandedSections.plugin }">
              <div class="oc-collapsible-header" @click="toggleOcSection('plugin')">
                <span class="oc-collapse-icon">{{ ocExpandedSections.plugin ? '&#9660;' : '&#9654;' }}</span>
                <span>Plugins <span class="oc-count-badge" v-if="ocGuiState.plugins.length">{{ ocGuiState.plugins.length }}</span></span>
              </div>
              <div class="oc-collapsible-body" v-if="ocExpandedSections.plugin">
                <div v-for="(plugin, idx) in ocGuiState.plugins" :key="idx" class="oc-kv-row">
                  <input type="text" v-model="ocGuiState.plugins[idx]" class="input-field" placeholder="插件名称或路径" @input="ocGuiToRaw" />
                  <button class="btn-icon danger" @click="removeOcPlugin(idx)" title="删除">
                    <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                  </button>
                </div>
                <button class="btn secondary btn-small" @click="addOcPlugin">+ 添加 Plugin</button>
              </div>
            </div>

            <!-- Advanced JSON -->
            <div class="oc-collapsible" :class="{ expanded: ocExpandedSections.advanced }">
              <div class="oc-collapsible-header" @click="toggleOcSection('advanced')">
                <span class="oc-collapse-icon">{{ ocExpandedSections.advanced ? '&#9660;' : '&#9654;' }}</span>
                <span>高级 JSON (完整编辑)</span>
              </div>
              <div class="oc-collapsible-body" v-if="ocExpandedSections.advanced">
                <p class="field-hint" style="margin-bottom: 8px;">直接编辑完整 JSON。结构化 GUI 中编辑的字段会自动同步到这里；手动修改 JSON 后请点击"从 JSON 同步到面板"以更新上方结构化区域。</p>
                <textarea
                  v-model="editingOpenCodeConfig"
                  class="input-field json-config-textarea"
                  spellcheck="false"
                  rows="8"
                  placeholder='例如: { "provider": { "openai": { "options": { "apiKey": "..." } } }, "model": "openai/gpt-5.4", "mcp": {}, "agent": {}, "permission": {} }'
                  @input="onAdvancedJsonInput"
                ></textarea>
                <div class="json-config-status" :class="{ error: !!openCodeConfigError, success: !openCodeConfigError && editingOpenCodeConfig.trim() }">
                  <span v-if="!editingOpenCodeConfig.trim()"></span>
                  <span v-else-if="openCodeConfigError">{{ openCodeConfigError }}</span>
                  <span v-else>JSON 格式正确</span>
                </div>
                <button class="btn secondary btn-small" @click="rawToOcGui" style="margin-top: 6px;">从 JSON 同步到面板</button>
              </div>
            </div>
          </div>
        </div>

        <div class="dialog-actions">
          <button class="btn secondary" @click="showPresetDialog = false" :disabled="loading">取消</button>
          <button class="btn primary" @click="handleSavePreset" :disabled="(!isEditingPreset && !editingPresetName) || !!openCodeConfigError || loading">
            {{ loading ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Close } from '@element-plus/icons-vue'
import { useToast } from '../composables/useToast'
import { GetProvider, SaveProvider, SavePreset, DeletePreset, GetUrlHistory, AddUrlToHistory, RemoveUrlFromHistory } from '../../wailsjs/go/config/ConfigService'
import { GetProviderExportJSON, SaveProviderFromJSON } from '../../wailsjs/go/main/App'
import { HasAPIKey, GetAPIKey, SetAPIKey, DeleteAPIKey, Save as SaveSecrets } from '../../wailsjs/go/secrets/SecretsService'
import { config } from '../../wailsjs/go/models'

const props = defineProps<{
  name: string
}>()

const route = useRoute()
const router = useRouter()
const { showSuccess, showError } = useToast()
const loading = ref(false)
const providerName = props.name || route.params.name as string

const provider = ref<config.Provider | null>(null)
const apiKeyState = ref({
  hasKey: false,
  isVisible: false,
  isEditing: false,
  isConfirmingDelete: false,
  inputVisible: false,
  actualKey: '',
  inputValue: ''
})

// URL History State
const urlHistory = ref<string[]>([])
const urlHistoryLoading = ref(false)
const previousBaseUrl = ref('')

// Preset Dialog State
const showPresetDialog = ref(false)
const isEditingPreset = ref(false)
const editingPresetName = ref('')
const editingPreset = ref(new config.Preset({
  name: '',
  model: '',
  parameters: new config.Parameters({})
}))
const thinkingType = ref('')
const thinkingBudget = ref<number | undefined>(undefined)
const editingPresetTarget = ref('')
const editingOpenCodeConfig = ref('')
const openCodeConfigError = ref('')

// Context Window Config State
const contextWindowModel = ref<number | undefined>(undefined)
const contextWindowCompact = ref<number | undefined>(undefined)

// JSON Editor State
const showJsonEditor = ref(false)
const jsonEditorContent = ref('')
const jsonError = ref('')

const maskedApiKey = computed(() => {
  if (apiKeyState.value.isVisible) {
    return apiKeyState.value.actualKey
  }

  const suffix = apiKeyState.value.actualKey ? apiKeyState.value.actualKey.slice(-4) : '****'
  return `••••••••${suffix}`
})

const loadProvider = async () => {
  try {
    provider.value = await GetProvider(providerName)
    previousBaseUrl.value = provider.value?.base_url || ''
    await loadUrlHistory()
  } catch (err) {
    console.error('Failed to load provider:', err)
    showError('加载失败: ' + err)
    router.push('/providers')
  }
}

const loadApiKey = async () => {
  apiKeyState.value = {
    hasKey: false,
    isVisible: false,
    isEditing: false,
    isConfirmingDelete: false,
    inputVisible: false,
    actualKey: '',
    inputValue: ''
  }

  if (!provider.value || provider.value.auth_key === 'OAUTH') {
    return
  }

  try {
    const hasKey = await HasAPIKey(providerName)
    apiKeyState.value.hasKey = hasKey
    if (hasKey) {
      apiKeyState.value.actualKey = await GetAPIKey(providerName)
    }
  } catch (err) {
    console.error('Failed to load API key:', err)
    showError('加载 API 密钥失败: ' + err)
  }
}

const toggleKeyVisibility = async () => {
  if (!apiKeyState.value.isVisible) {
    try {
      apiKeyState.value.actualKey = await GetAPIKey(providerName)
      apiKeyState.value.isVisible = true
    } catch (err) {
      console.error('Failed to get API key:', err)
      showError('读取 API 密钥失败: ' + err)
    }
    return
  }

  apiKeyState.value.isVisible = false
}

const startKeyEdit = () => {
  apiKeyState.value.inputValue = apiKeyState.value.actualKey
  apiKeyState.value.inputVisible = true
  apiKeyState.value.isEditing = true
  apiKeyState.value.isConfirmingDelete = false
}

const cancelKeyEdit = () => {
  apiKeyState.value.inputValue = ''
  apiKeyState.value.inputVisible = false
  apiKeyState.value.isEditing = false
}

const saveApiKey = async () => {
  if (!apiKeyState.value.inputValue) {
    return
  }

  const wasEditing = apiKeyState.value.isEditing
  loading.value = true
  try {
    await SetAPIKey(providerName, apiKeyState.value.inputValue)
    await SaveSecrets()
    apiKeyState.value.hasKey = true
    apiKeyState.value.actualKey = apiKeyState.value.inputValue
    apiKeyState.value.inputValue = ''
    apiKeyState.value.inputVisible = false
    apiKeyState.value.isEditing = false
    apiKeyState.value.isVisible = false
    apiKeyState.value.isConfirmingDelete = false
    showSuccess(wasEditing ? 'API 密钥已更新' : 'API 密钥已保存')
  } catch (err) {
    console.error('Failed to save API key:', err)
    showError('保存 API 密钥失败: ' + err)
  } finally {
    loading.value = false
  }
}

const deleteApiKey = async () => {
  loading.value = true
  try {
    await DeleteAPIKey(providerName)
    await SaveSecrets()
    apiKeyState.value = {
      hasKey: false,
      isVisible: false,
      isEditing: false,
      isConfirmingDelete: false,
      inputVisible: false,
      actualKey: '',
      inputValue: ''
    }
    showSuccess('API 密钥已删除')
  } catch (err) {
    console.error('Failed to delete API key:', err)
    showError('删除 API 密钥失败: ' + err)
  } finally {
    loading.value = false
  }
}

const loadUrlHistory = async () => {
  urlHistoryLoading.value = true
  try {
    if (typeof GetUrlHistory === 'function') {
      const history = await GetUrlHistory(providerName)
      urlHistory.value = history || []
    } else {
      console.warn('GetUrlHistory API not available yet. Please run wails dev to generate bindings.')
    }
  } catch (err) {
    console.error('Failed to load URL history:', err)
    // Non-critical error, don't show toast
  } finally {
    urlHistoryLoading.value = false
  }
}

const handleSaveProvider = async () => {
  if (!provider.value) return
  loading.value = true
  try {
    await SaveProvider(providerName, provider.value)
    // Add current URL to history if it changed and is not empty
    const currentUrl = provider.value.base_url || ''
    if (currentUrl && currentUrl !== previousBaseUrl.value) {
      try {
        if (typeof AddUrlToHistory === 'function') {
          await AddUrlToHistory(providerName, currentUrl)
          previousBaseUrl.value = currentUrl
          await loadUrlHistory()
        } else {
          console.warn('AddUrlToHistory API not available yet. Please run wails dev to generate bindings.')
        }
      } catch (historyErr) {
        console.error('Failed to add URL to history:', historyErr)
        // Non-critical error, don't fail the save
      }
    }
    await loadApiKey()
    showSuccess('保存成功')
  } catch (err) {
    console.error('Failed to save provider:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

const openAddPresetDialog = () => {
  isEditingPreset.value = false
  editingPresetName.value = ''
  editingPreset.value = new config.Preset({
    name: '',
    model: '',
    parameters: new config.Parameters({})
  })
  thinkingType.value = ''
  thinkingBudget.value = undefined
  contextWindowModel.value = undefined
  contextWindowCompact.value = undefined
  editingPresetTarget.value = ''
  editingOpenCodeConfig.value = ''
  openCodeConfigError.value = ''
  // Reset OpenCode GUI state
  ocGuiState.value = {
    model: '',
    providerRaw: '',
    providerError: '',
    mcpServers: [],
    agents: [],
    permissions: [],
    instructions: [],
    plugins: [],
    unknownFieldsRaw: '',
    $schema: '',
  }
  ocExpandedSections.value = { provider: true, mcp: false, agent: false, permission: false, instructions: false, plugin: false, advanced: false }
  showPresetDialog.value = true
}

const openEditPresetDialog = (name: string, preset: config.Preset) => {
  isEditingPreset.value = true
  editingPresetName.value = name

  // Deep copy to avoid modifying original before save
  const presetCopy = JSON.parse(JSON.stringify(preset))
  editingPreset.value = new config.Preset(presetCopy)

  if (editingPreset.value.parameters?.thinking) {
    thinkingType.value = editingPreset.value.parameters.thinking.type || ''
    thinkingBudget.value = editingPreset.value.parameters.thinking.budgetTokens
  } else {
    thinkingType.value = ''
    thinkingBudget.value = undefined
  }

  // Load context window config
  if (editingPreset.value.parameters?.context_window) {
    contextWindowModel.value = editingPreset.value.parameters.context_window.model_context_window
    contextWindowCompact.value = editingPreset.value.parameters.context_window.model_auto_compact_token_limit
  } else {
    contextWindowModel.value = undefined
    contextWindowCompact.value = undefined
  }

  // Load target and opencode_config
  editingPresetTarget.value = presetCopy.target || ''
  // Normalize opencode_config: Wails may deliver as number[] (json.RawMessage), string, or undefined
  editingOpenCodeConfig.value = normalizeOpenCodeConfigValue(presetCopy.opencode_config)
  openCodeConfigError.value = ''
  // Validate initial opencode_config
  if (editingOpenCodeConfig.value.trim()) {
    validateOpenCodeConfig()
  }

  // Parse OpenCode config into structured GUI state
  rawToOcGui()

  showPresetDialog.value = true
}

const handleSavePreset = async () => {
  const nameToSave = isEditingPreset.value ? editingPresetName.value : editingPresetName.value
  if (!nameToSave) return

  // Validate opencode_config JSON before saving
  if (editingOpenCodeConfig.value.trim()) {
    validateOpenCodeConfig()
    if (openCodeConfigError.value) {
      showError('opencode_config JSON 格式错误: ' + openCodeConfigError.value)
      return
    }
  }

  // Apply thinking config
  if (!editingPreset.value.parameters) {
    editingPreset.value.parameters = new config.Parameters({})
  }

  if (thinkingType.value) {
    editingPreset.value.parameters.thinking = new config.ThinkingConfig({
      type: thinkingType.value,
      budgetTokens: thinkingType.value === 'enabled' ? thinkingBudget.value : undefined
    })
  } else {
    editingPreset.value.parameters.thinking = undefined
  }

  // Apply context window config
  if (contextWindowModel.value !== undefined || contextWindowCompact.value !== undefined) {
    editingPreset.value.parameters.context_window = {
      model_context_window: contextWindowModel.value,
      model_auto_compact_token_limit: contextWindowCompact.value
    }
  } else {
    editingPreset.value.parameters.context_window = undefined
  }

  // Ensure name is set inside preset object
  editingPreset.value.name = nameToSave

  // Apply target
  const presetAny = editingPreset.value as any
  presetAny.target = editingPresetTarget.value || undefined

  // Apply opencode_config: store the raw JSON string
  if (editingOpenCodeConfig.value.trim()) {
    presetAny.opencode_config = editingOpenCodeConfig.value.trim()
  } else {
    presetAny.opencode_config = undefined
  }

  try {
    loading.value = true
    await SavePreset(providerName, nameToSave, editingPreset.value)
    showPresetDialog.value = false
    await loadProvider()
    showSuccess('保存预设成功')
  } catch (err) {
    console.error('Failed to save preset:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

// OpenCode Config validation
const validateOpenCodeConfig = () => {
  const val = editingOpenCodeConfig.value.trim()
  if (!val) {
    openCodeConfigError.value = ''
    return
  }
  try {
    const parsed = JSON.parse(val)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      openCodeConfigError.value = '必须是有效的 JSON 对象'
      return
    }
    openCodeConfigError.value = ''
  } catch (err: any) {
    openCodeConfigError.value = err.message || '无效的 JSON'
  }
}

// ============ OpenCode Structured GUI State & Logic ============

interface OcToolEntry {
  name: string
  enabled: boolean
}

interface OcKvPair {
  key: string
  value: string
}

interface OcMcpEntry {
  name: string
  type: 'remote' | 'local'
  url: string
  commandArgs: string[]
  headers: OcKvPair[]
  environment: OcKvPair[]
  oauth: boolean
  extraRaw: string
}

interface OcAgentEntry {
  name: string
  description: string
  mode: 'primary' | 'subagent'
  model: string
  color: string
  prompt: string
  tools: OcToolEntry[]
  extraRaw: string
}

interface OcPermEntry {
  key: string
  value: string
}

const ocExpandedSections = ref<Record<string, boolean>>({
  provider: true,
  mcp: false,
  agent: false,
  permission: false,
  instructions: false,
  plugin: false,
  advanced: false,
})

const toggleOcSection = (section: string) => {
  ocExpandedSections.value[section] = !ocExpandedSections.value[section]
}

const ocGuiState = ref<{
  model: string
  providerRaw: string
  providerError: string
  mcpServers: OcMcpEntry[]
  agents: OcAgentEntry[]
  permissions: OcPermEntry[]
  instructions: string[]
  plugins: string[]
  unknownFieldsRaw: string
  $schema: string
}>({
  model: '',
  providerRaw: '',
  providerError: '',
  mcpServers: [],
  agents: [],
  permissions: [],
  instructions: [],
  plugins: [],
  unknownFieldsRaw: '',
  $schema: '',
})

// Known top-level keys that have their own structured sections
const OC_KNOWN_KEYS = ['model', 'provider', 'mcp', 'agent', 'permission', 'instructions', 'plugin', '$schema']

// Parse raw JSON into structured GUI state
const rawToOcGui = () => {
  const raw = editingOpenCodeConfig.value.trim()
  if (!raw) {
    ocGuiState.value = {
      model: '',
      providerRaw: '',
      providerError: '',
      mcpServers: [],
      agents: [],
      permissions: [],
      instructions: [],
      plugins: [],
      unknownFieldsRaw: '',
      $schema: '',
    }
    return
  }
  try {
    const obj = JSON.parse(raw)
    if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) return

    // Model
    ocGuiState.value.model = typeof obj.model === 'string' ? obj.model : ''

    // Provider (preserved internally, not shown as JSON textarea)
    if (obj.provider && typeof obj.provider === 'object') {
      const pretty = JSON.stringify(obj.provider, null, 2)
      ocGuiState.value.providerRaw = pretty
      ocGuiState.value.providerError = ''
    } else {
      ocGuiState.value.providerRaw = ''
      ocGuiState.value.providerError = ''
    }

    // MCP Servers
    const mcpServers: OcMcpEntry[] = []
    if (obj.mcp && typeof obj.mcp === 'object' && !Array.isArray(obj.mcp)) {
      const MCP_KNOWN_KEYS = new Set(['type', 'url', 'command', 'headers', 'environment', 'oauth'])
      for (const [name, entry] of Object.entries(obj.mcp as Record<string, any>)) {
        if (!entry || typeof entry !== 'object') continue
        // Collect unknown fields for this MCP entry
        const extra: Record<string, any> = {}
        for (const [k, v] of Object.entries(entry)) {
          if (!MCP_KNOWN_KEYS.has(k)) {
            extra[k] = v
          }
        }
        // Parse command into string array
        let commandArgs: string[] = []
        if (Array.isArray(entry.command)) {
          commandArgs = entry.command.map((s: any) => String(s))
        } else if (typeof entry.command === 'string' && entry.command.trim()) {
          commandArgs = [entry.command]
        }
        // Parse headers into kv pairs
        const headers: OcKvPair[] = []
        if (entry.headers && typeof entry.headers === 'object' && !Array.isArray(entry.headers)) {
          for (const [k, v] of Object.entries(entry.headers as Record<string, any>)) {
            headers.push({ key: k, value: String(v) })
          }
        }
        // Parse environment into kv pairs
        const environment: OcKvPair[] = []
        if (entry.environment && typeof entry.environment === 'object' && !Array.isArray(entry.environment)) {
          for (const [k, v] of Object.entries(entry.environment as Record<string, any>)) {
            environment.push({ key: k, value: String(v) })
          }
        }
        mcpServers.push({
          name,
          type: entry.type === 'local' ? 'local' : 'remote',
          url: entry.url || '',
          commandArgs,
          headers,
          environment,
          oauth: !!entry.oauth,
          extraRaw: Object.keys(extra).length > 0 ? JSON.stringify(extra, null, 2) : '',
        })
      }
    }
    ocGuiState.value.mcpServers = mcpServers

    // Agents
    const agents: OcAgentEntry[] = []
    if (obj.agent && typeof obj.agent === 'object' && !Array.isArray(obj.agent)) {
      const AGENT_KNOWN_KEYS = new Set(['description', 'mode', 'model', 'color', 'prompt', 'tools'])
      for (const [name, entry] of Object.entries(obj.agent as Record<string, any>)) {
        if (!entry || typeof entry !== 'object') continue
        // Collect unknown fields for this agent entry
        const extra: Record<string, any> = {}
        for (const [k, v] of Object.entries(entry)) {
          if (!AGENT_KNOWN_KEYS.has(k)) {
            extra[k] = v
          }
        }
        // Parse tools into structured array
        const tools: OcToolEntry[] = []
        if (entry.tools && typeof entry.tools === 'object' && !Array.isArray(entry.tools)) {
          for (const [toolName, toolVal] of Object.entries(entry.tools as Record<string, any>)) {
            tools.push({ name: toolName, enabled: toolVal !== false })
          }
        }
        agents.push({
          name,
          description: entry.description || '',
          mode: entry.mode === 'primary' ? 'primary' : 'subagent',
          model: entry.model || '',
          color: entry.color || '',
          prompt: entry.prompt || '',
          tools,
          extraRaw: Object.keys(extra).length > 0 ? JSON.stringify(extra, null, 2) : '',
        })
      }
    }
    ocGuiState.value.agents = agents

    // Permissions
    const permissions: OcPermEntry[] = []
    if (obj.permission && typeof obj.permission === 'object' && !Array.isArray(obj.permission)) {
      for (const [key, val] of Object.entries(obj.permission as Record<string, any>)) {
        permissions.push({ key, value: String(val) })
      }
    }
    ocGuiState.value.permissions = permissions

    // Instructions
    if (Array.isArray(obj.instructions)) {
      ocGuiState.value.instructions = obj.instructions.filter((s: any) => typeof s === 'string')
    } else {
      ocGuiState.value.instructions = []
    }

    // Plugins
    if (Array.isArray(obj.plugin)) {
      ocGuiState.value.plugins = obj.plugin.map((p: any) => typeof p === 'string' ? p : JSON.stringify(p))
    } else {
      ocGuiState.value.plugins = []
    }

    // $schema
    if (typeof obj['$schema'] === 'string') {
      ocGuiState.value['$schema'] = obj['$schema']
    } else {
      ocGuiState.value['$schema'] = ''
    }

    // Unknown fields
    const unknownKeys = Object.keys(obj).filter(k => !OC_KNOWN_KEYS.includes(k))
    if (unknownKeys.length > 0) {
      const unknownObj: Record<string, any> = {}
      for (const k of unknownKeys) {
        unknownObj[k] = obj[k]
      }
      ocGuiState.value.unknownFieldsRaw = JSON.stringify(unknownObj, null, 2)
    } else {
      ocGuiState.value.unknownFieldsRaw = ''
    }
  } catch {
    // If raw JSON is invalid, don't blow away GUI state
  }
}

// Serialize GUI state back to raw JSON string
const ocGuiToRaw = () => {
  // Validate provider sub-JSON (internal)
  if (ocGuiState.value.providerRaw.trim()) {
    try {
      JSON.parse(ocGuiState.value.providerRaw)
      ocGuiState.value.providerError = ''
    } catch (e: any) {
      ocGuiState.value.providerError = e.message || '无效 JSON'
    }
  } else {
    ocGuiState.value.providerError = ''
  }

  const result: Record<string, any> = {}

  // Model
  if (ocGuiState.value.model.trim()) {
    result.model = ocGuiState.value.model.trim()
  }

  // Provider (preserved internally)
  if (ocGuiState.value.providerRaw.trim()) {
    try {
      result.provider = JSON.parse(ocGuiState.value.providerRaw)
    } catch { /* skip invalid provider JSON */ }
  }

  // MCP
  if (ocGuiState.value.mcpServers.length > 0) {
    const mcp: Record<string, any> = {}
    for (const server of ocGuiState.value.mcpServers) {
      const name = server.name.trim()
      if (!name) continue
      const entry: Record<string, any> = { type: server.type }
      if (server.type === 'remote' && server.url.trim()) {
        entry.url = server.url.trim()
      }
      if (server.type === 'local' && server.commandArgs.length > 0) {
        const filtered = server.commandArgs.filter(a => a.trim())
        if (filtered.length > 0) entry.command = filtered
      }
      // Serialize structured headers
      const headersWithKeys = server.headers.filter(h => h.key.trim())
      if (headersWithKeys.length > 0) {
        const headersObj: Record<string, string> = {}
        for (const h of headersWithKeys) {
          headersObj[h.key.trim()] = h.value
        }
        entry.headers = headersObj
      }
      // Serialize structured environment
      const envWithKeys = server.environment.filter(e => e.key.trim())
      if (envWithKeys.length > 0) {
        const envObj: Record<string, string> = {}
        for (const e of envWithKeys) {
          envObj[e.key.trim()] = e.value
        }
        entry.environment = envObj
      }
      if (server.oauth) {
        entry.oauth = true
      }
      // Merge back unknown/extra fields for this MCP entry
      if (server.extraRaw.trim()) {
        try {
          const extra = JSON.parse(server.extraRaw)
          if (typeof extra === 'object' && !Array.isArray(extra)) {
            Object.assign(entry, extra)
          }
        } catch { /* skip invalid extra fields */ }
      }
      mcp[name] = entry
    }
    if (Object.keys(mcp).length > 0) {
      result.mcp = mcp
    }
  }

  // Agent
  if (ocGuiState.value.agents.length > 0) {
    const agent: Record<string, any> = {}
    for (const a of ocGuiState.value.agents) {
      const name = a.name.trim()
      if (!name) continue
      const entry: Record<string, any> = {}
      if (a.description.trim()) entry.description = a.description.trim()
      if (a.mode) entry.mode = a.mode
      if (a.model.trim()) entry.model = a.model.trim()
      if (a.color.trim()) entry.color = a.color.trim()
      if (a.prompt.trim()) entry.prompt = a.prompt.trim()
      // Serialize structured tools
      const toolsWithNames = a.tools.filter(t => t.name.trim())
      if (toolsWithNames.length > 0) {
        const toolsObj: Record<string, boolean> = {}
        for (const t of toolsWithNames) {
          toolsObj[t.name.trim()] = t.enabled
        }
        entry.tools = toolsObj
      }
      // Merge back unknown/extra fields for this agent entry
      if (a.extraRaw.trim()) {
        try {
          const extra = JSON.parse(a.extraRaw)
          if (typeof extra === 'object' && !Array.isArray(extra)) {
            Object.assign(entry, extra)
          }
        } catch { /* skip invalid extra fields */ }
      }
      agent[name] = entry
    }
    if (Object.keys(agent).length > 0) {
      result.agent = agent
    }
  }

  // Permission
  if (ocGuiState.value.permissions.length > 0) {
    const permission: Record<string, string> = {}
    for (const p of ocGuiState.value.permissions) {
      if (p.key.trim()) {
        permission[p.key.trim()] = p.value
      }
    }
    if (Object.keys(permission).length > 0) {
      result.permission = permission
    }
  }

  // Instructions
  const instrs = ocGuiState.value.instructions.filter(s => s.trim())
  if (instrs.length > 0) {
    result.instructions = instrs
  }

  // Plugins
  const plugins = ocGuiState.value.plugins.filter(s => s.trim())
  if (plugins.length > 0) {
    result.plugin = plugins
  }

  // Unknown fields - merge them back
  if (ocGuiState.value.unknownFieldsRaw.trim()) {
    try {
      const unknowns = JSON.parse(ocGuiState.value.unknownFieldsRaw)
      if (typeof unknowns === 'object' && !Array.isArray(unknowns)) {
        Object.assign(result, unknowns)
      }
    } catch { /* skip invalid unknown fields */ }
  }

  // $schema - preserve it
  if (ocGuiState.value['$schema'].trim()) {
    result['$schema'] = ocGuiState.value['$schema'].trim()
  }

  editingOpenCodeConfig.value = Object.keys(result).length > 0 ? JSON.stringify(result, null, 2) : ''
  validateOpenCodeConfig()
}

// Add/Remove MCP
const addOcMcp = () => {
  ocGuiState.value.mcpServers.push({ name: '', type: 'remote', url: '', commandArgs: [], headers: [], environment: [], oauth: false, extraRaw: '' })
  if (!ocExpandedSections.value.mcp) ocExpandedSections.value.mcp = true
}
const removeOcMcp = (idx: number) => {
  ocGuiState.value.mcpServers.splice(idx, 1)
  ocGuiToRaw()
}

// Add/Remove Agent
const addOcAgent = () => {
  ocGuiState.value.agents.push({ name: '', description: '', mode: 'subagent', model: '', color: '', prompt: '', tools: [], extraRaw: '' })
  if (!ocExpandedSections.value.agent) ocExpandedSections.value.agent = true
}
const removeOcAgent = (idx: number) => {
  ocGuiState.value.agents.splice(idx, 1)
  ocGuiToRaw()
}

// Add/Remove Permission
const addOcPermission = () => {
  ocGuiState.value.permissions.push({ key: '', value: 'allow' })
  if (!ocExpandedSections.value.permission) ocExpandedSections.value.permission = true
}
const removeOcPermission = (idx: number) => {
  ocGuiState.value.permissions.splice(idx, 1)
  ocGuiToRaw()
}

// Add/Remove Instruction
const addOcInstruction = () => {
  ocGuiState.value.instructions.push('')
  if (!ocExpandedSections.value.instructions) ocExpandedSections.value.instructions = true
}
const removeOcInstruction = (idx: number) => {
  ocGuiState.value.instructions.splice(idx, 1)
  ocGuiToRaw()
}

// Add/Remove Plugin
const addOcPlugin = () => {
  ocGuiState.value.plugins.push('')
  if (!ocExpandedSections.value.plugin) ocExpandedSections.value.plugin = true
}
const removeOcPlugin = (idx: number) => {
  ocGuiState.value.plugins.splice(idx, 1)
  ocGuiToRaw()
}

// Handler for manual edits in the advanced JSON textarea
const onAdvancedJsonInput = () => {
  validateOpenCodeConfig()
}

const handleDeletePreset = async (presetName: string) => {
  if (confirm(`确定要删除预设 "${presetName}" 吗？`)) {
    try {
      loading.value = true
      await DeletePreset(providerName, presetName)
      await loadProvider()
      showSuccess('删除预设成功')
    } catch (err) {
      console.error('Failed to delete preset:', err)
      showError('删除失败: ' + err)
    } finally {
      loading.value = false
    }
  }
}

const queryUrlHistory = (queryString: string, cb: (results: { value: string }[]) => void) => {
  const results = queryString
    ? urlHistory.value.filter(url => url.toLowerCase().includes(queryString.toLowerCase()))
    : urlHistory.value
  cb(results.map(url => ({ value: url })))
}

const onBaseUrlSelect = (item: { value: string }) => {
  if (provider.value) {
    provider.value.base_url = item.value
  }
}

const onBaseUrlBlur = async () => {
  const currentUrl = provider.value?.base_url?.trim() || ''
  if (currentUrl && currentUrl !== previousBaseUrl.value) {
    try {
      if (typeof AddUrlToHistory === 'function') {
        await AddUrlToHistory(providerName, currentUrl)
        previousBaseUrl.value = currentUrl
        await loadUrlHistory()
      } else {
        console.warn('AddUrlToHistory API not available yet. Please run wails dev to generate bindings.')
      }
    } catch (historyErr) {
      console.error('Failed to add URL to history:', historyErr)
    }
  }
}

const handleBaseUrlChange = (value: string) => {
  // Store the previous value when URL changes
  previousBaseUrl.value = provider.value?.base_url || ''
}

const handleRemoveUrl = async (url: string) => {
  if (!confirm(`确定要从历史记录中删除 "${url}" 吗？`)) {
    return
  }
  try {
    if (typeof RemoveUrlFromHistory === 'function') {
      await RemoveUrlFromHistory(providerName, url)
      // Update local state
      urlHistory.value = urlHistory.value.filter(u => u !== url)
      showSuccess('删除成功')
    } else {
      console.warn('RemoveUrlFromHistory API not available yet. Please run wails dev to generate bindings.')
    }
  } catch (err) {
    console.error('Failed to remove URL from history:', err)
    showError('删除失败: ' + err)
  }
}

const handleSaveBaseUrl = async () => {
  const currentUrl = provider.value?.base_url?.trim() || ''
  if (!currentUrl) {
    showError('请输入 URL')
    return
  }
  try {
    if (typeof AddUrlToHistory === 'function') {
      await AddUrlToHistory(providerName, currentUrl)
      previousBaseUrl.value = currentUrl
      await loadUrlHistory()
      showSuccess('URL 已保存到历史记录')
    } else {
      console.warn('AddUrlToHistory API not available yet. Please run wails dev to generate bindings.')
    }
  } catch (err) {
    console.error('Failed to save URL to history:', err)
    showError('保存失败: ' + err)
  }
}

// Context Window quick preset methods
const setContextWindow = (value: number) => {
  contextWindowModel.value = value
  // Auto-set compact limit to ~10% of context window
  if (!contextWindowCompact.value) {
    contextWindowCompact.value = Math.floor(value * 0.1)
  }
}

const setCompactLimit = (value: number) => {
  contextWindowCompact.value = value
}

// Format context size for display (e.g., 1048576 -> "1M")
const formatContextSize = (tokens: number): string => {
  if (tokens >= 1000000) {
    return `${(tokens / 1000000).toFixed(1)}M`
  } else if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(0)}K`
  }
  return `${tokens}`
}

// Normalize opencode_config value from Wails binding to a string.
// Wails serializes json.RawMessage as number[] (byte array), but the GUI needs a string.
const normalizeOpenCodeConfigValue = (raw: string | number[] | Uint8Array | undefined | null): string => {
  if (raw === undefined || raw === null) return ''
  if (typeof raw === 'string') return raw
  if (Array.isArray(raw) || (raw instanceof Uint8Array)) {
    const bytes = raw instanceof Uint8Array ? raw : new Uint8Array(raw)
    return new TextDecoder().decode(bytes)
  }
  return String(raw)
}

// Summarize opencode_config for preset card display
const ocConfigSummary = (raw: string | number[] | undefined): string => {
  if (!raw) return '(empty)'
  const str = normalizeOpenCodeConfigValue(raw)
  if (!str.trim()) return '(empty)'
  try {
    const obj = JSON.parse(str)
    if (typeof obj !== 'object' || Array.isArray(obj)) return str.slice(0, 60)
    const parts: string[] = []
    if (obj.model) parts.push(`model: ${obj.model}`)
    if (obj.provider && typeof obj.provider === 'object') parts.push(`providers: ${Object.keys(obj.provider).join(', ')}`)
    if (obj.mcp && typeof obj.mcp === 'object') parts.push(`mcp: ${Object.keys(obj.mcp).length} servers`)
    if (obj.agent && typeof obj.agent === 'object') parts.push(`agents: ${Object.keys(obj.agent).length}`)
    if (obj.permission && typeof obj.permission === 'object') parts.push(`perms: ${Object.keys(obj.permission).length}`)
    if (Array.isArray(obj.instructions)) parts.push(`instr: ${obj.instructions.length}`)
    if (Array.isArray(obj.plugin) && obj.plugin.length) parts.push(`plugins: ${obj.plugin.length}`)
    return parts.length > 0 ? parts.join(' | ') : '(no structured fields)'
  } catch {
    return str.slice(0, 60) + (str.length > 60 ? '...' : '')
  }
}

// JSON Editor methods
const openJsonEditor = async () => {
  jsonEditorContent.value = ''
  jsonError.value = ''
  showJsonEditor.value = true
  try {
    const json = await GetProviderExportJSON(providerName)
    jsonEditorContent.value = json
  } catch (err) {
    console.error('Failed to get provider JSON:', err)
    showError('加载 JSON 失败: ' + err)
    showJsonEditor.value = false
  }
}

const validateJson = () => {
  if (!jsonEditorContent.value.trim()) {
    jsonError.value = ''
    return
  }
  try {
    JSON.parse(jsonEditorContent.value)
    jsonError.value = ''
  } catch (err: any) {
    jsonError.value = err.message || '无效的 JSON'
  }
}

const handleSaveJson = async () => {
  validateJson()
  if (jsonError.value) return

  loading.value = true
  try {
    await SaveProviderFromJSON(providerName, jsonEditorContent.value)
    showJsonEditor.value = false
    await loadProvider()
    await loadApiKey()
    showSuccess('JSON 保存成功')
  } catch (err) {
    console.error('Failed to save provider from JSON:', err)
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

watch(jsonEditorContent, () => {
  validateJson()
})

// When target switches to opencode, initialize GUI state from raw config
watch(editingPresetTarget, (newVal) => {
  if (newVal === 'opencode') {
    rawToOcGui()
  }
})

onMounted(async () => {
  if (providerName) {
    await loadProvider()
    await loadApiKey()
  } else {
    router.push('/providers')
  }
})
</script>

<style scoped>
.provider-detail {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.breadcrumb {
  font-size: 16px;
  color: #8899aa;
  display: flex;
  align-items: center;
  gap: 8px;
}

.back-link {
  color: #4fc3f7;
  text-decoration: none;
  transition: color 0.15s ease;
}

.back-link:hover {
  color: #7bd4f9;
  text-decoration: underline;
}

.current {
  color: #e0e6ed;
  font-weight: 600;
}

.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.card-header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #e0e6ed;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.card-title-group {
  display: flex;
  align-items: center;
  gap: 12px;
}

.status-badge {
  font-size: 12px;
  padding: 4px 8px;
  border-radius: 4px;
  font-weight: 600;
}

.status-configured {
  background-color: rgba(102, 187, 106, 0.1);
  color: #66bb6a;
  border: 1px solid rgba(102, 187, 106, 0.2);
}

.status-unconfigured {
  background-color: rgba(255, 167, 38, 0.1);
  color: #ffa726;
  border: 1px solid rgba(255, 167, 38, 0.2);
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 20px;
}

.form-group {
  margin-bottom: 16px;
}

.form-group label {
  display: block;
  margin-bottom: 8px;
  color: #8899aa;
  font-size: 14px;
}

.field-hint {
  margin: 6px 0 0;
  color: #5a6a7a;
  font-size: 12px;
  line-height: 1.5;
}

.oauth-notice {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 18px;
  background: rgba(79, 195, 247, 0.06);
  border: 1px solid rgba(79, 195, 247, 0.2);
  border-radius: 8px;
  color: #a0d8ef;
  font-size: 14px;
}

.key-display {
  display: flex;
  align-items: center;
  gap: 12px;
  background-color: #0f1219;
  padding: 12px;
  border-radius: 6px;
  border: 1px solid #2a2f3e;
  flex-wrap: wrap;
}

.key-text {
  font-family: monospace;
  font-size: 14px;
  color: #e0e6ed;
  flex: 1;
  word-break: break-all;
}

.key-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.confirm-text {
  font-size: 12px;
  color: #ffa726;
  font-weight: 600;
  white-space: nowrap;
}

.api-key-input-group {
  margin-bottom: 0;
}

.key-input-row {
  display: flex;
  gap: 12px;
  align-items: center;
}

.input-field {
  width: 100%;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #e0e6ed;
  padding: 10px 12px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  transition: all 0.15s ease;
  outline: none;
  box-sizing: border-box;
}

.input-field:focus {
  border-color: #4fc3f7;
}

/* URL Autocomplete Styles */
.url-input-group {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}

.url-autocomplete-wrapper {
  position: relative;
  flex: 1;
}

.url-autocomplete {
  width: 100%;
}

.url-save-btn {
  margin-top: 1px;
  white-space: nowrap;
  padding: 8px 16px;
  height: 38px;
}

.url-autocomplete :deep(.el-input__wrapper) {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 8px 12px;
  box-shadow: none;
  transition: all 0.15s ease;
}

.url-autocomplete :deep(.el-input__wrapper:hover) {
  border-color: #4fc3f7;
}

.url-autocomplete :deep(.el-input__wrapper.is-focus) {
  border-color: #4fc3f7;
  box-shadow: 0 0 0 1px #4fc3f7;
}

.url-autocomplete :deep(.el-input__inner) {
  color: #e0e6ed;
  font-size: 14px;
  font-family: inherit;
}

.url-autocomplete :deep(.el-input__inner::placeholder) {
  color: #5a6a7a;
}

/* Dropdown Styles */
.url-history-dropdown {
  background-color: #1a1f2e !important;
  border: 1px solid #2a2f3e !important;
  border-radius: 8px !important;
  padding: 8px !important;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4) !important;
}

.url-history-dropdown .el-autocomplete-suggestion__wrap {
  max-height: 300px !important;
}

.url-history-dropdown .el-autocomplete-suggestion__list {
  padding: 0 !important;
}

.url-history-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-radius: 6px;
  transition: background-color 0.15s ease;
}

.url-history-item:hover {
  background-color: rgba(79, 195, 247, 0.1);
}

.url-history-item .url-text {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #e0e6ed;
  font-size: 14px;
  font-family: monospace;
  word-break: break-all;
}

.url-history-item .url-delete-btn {
  color: #5a6a7a;
  font-size: 16px;
  cursor: pointer;
  transition: color 0.15s ease;
  flex-shrink: 0;
  margin-left: 8px;
}

.url-history-item .url-delete-btn:hover {
  color: #ef5350;
}

.url-empty-state {
  padding: 12px 16px;
  color: #8899aa;
  font-size: 14px;
  text-align: center;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
}

.section-header h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: #e0e6ed;
}

.migration-hint {
  margin: 6px 0 0;
  font-size: 12px;
  color: #5a6a7a;
  line-height: 1.5;
}

.migration-hint-box {
  margin-top: 8px;
  padding: 10px 14px;
  background: rgba(255, 167, 38, 0.06);
  border: 1px solid rgba(255, 167, 38, 0.15);
  border-radius: 6px;
}

.migration-hint-box-prominent {
  background: rgba(255, 167, 38, 0.10);
  border-color: rgba(255, 167, 38, 0.30);
}

.migration-hint-box .migration-hint {
  margin: 0 0 6px 0;
}

.migration-hint-strong {
  margin: 0;
  font-size: 12px;
  color: #ffa726;
  line-height: 1.5;
  font-weight: 500;
}

.migration-cta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
}

.migration-cta-text {
  font-size: 12px;
  color: #8899aa;
}

.migration-cta-btn {
  font-size: 12px;
  padding: 3px 10px;
  border-color: #4fc3f7;
  color: #4fc3f7;
}

.migration-cta-btn:hover {
  background: rgba(79, 195, 247, 0.1);
}

.presets-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(350px, 1fr));
  gap: 20px;
}

.preset-card {
  display: flex;
  flex-direction: column;
}

.preset-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid #2a2f3e;
}

.preset-name {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

.preset-actions {
  display: flex;
  gap: 4px;
}

.info-row {
  display: flex;
  margin-bottom: 12px;
  font-size: 14px;
}

.label {
  color: #8899aa;
  min-width: 60px;
}

.value {
  color: #e0e6ed;
}

.params-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.param-badge {
  background: rgba(90, 106, 122, 0.2);
  color: #8899aa;
  padding: 4px 8px;
  border-radius: 4px;
  font-size: 12px;
  border: 1px solid #2a2f3e;
}

.empty-state {
  grid-column: 1 / -1;
  text-align: center;
  padding: 40px;
  background: #1a1f2e;
  border: 1px dashed #2a2f3e;
  border-radius: 8px;
}

.muted {
  color: #5a6a7a;
}

/* Dialog Styles */
.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(15, 18, 25, 0.8);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  backdrop-filter: blur(4px);
}

.dialog {
  width: 100%;
  max-width: 600px;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

/* Expand dialog width when OpenCode target is selected */
.dialog:has(.opencode-gui-section) {
  max-width: 740px;
}

.preset-dialog {
  padding: 0;
}

.preset-dialog h2 {
  margin: 0;
  padding: 20px;
  border-bottom: 1px solid #2a2f3e;
  color: #e0e6ed;
}

.dialog-scroll-area {
  padding: 20px;
  overflow-y: auto;
  flex: 1;
}

.section-subtitle {
  margin: 24px 0 16px 0;
  font-size: 16px;
  color: #4fc3f7;
  border-bottom: 1px dashed #2a2f3e;
  padding-bottom: 8px;
}

.form-grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.checkbox-group-inline {
  display: flex;
  gap: 24px;
  margin: 16px 0;
}

.checkbox-label {
  display: flex;
  align-items: center;
  cursor: pointer;
  user-select: none;
}

.checkbox-label input {
  margin-right: 8px;
  width: 16px;
  height: 16px;
  accent-color: #4fc3f7;
}

.checkbox-text {
  color: #e0e6ed;
  font-size: 14px;
}

.dialog-actions {
  padding: 20px;
  border-top: 1px solid #2a2f3e;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

/* Buttons */
.btn {
  padding: 8px 16px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  border: none;
  outline: none;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn.primary {
  background: #4fc3f7;
  color: #0f1219;
}

.btn.primary:hover:not(:disabled) {
  background: #7bd4f9;
}

.btn.secondary {
  background: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn.secondary:hover:not(:disabled) {
  border-color: #5a6a7a;
  background: rgba(255, 255, 255, 0.05);
}

.btn.danger-outline {
  background: transparent;
  color: #ef5350;
  border: 1px solid #ef5350;
}

.btn.danger-outline:hover:not(:disabled) {
  background-color: rgba(239, 83, 80, 0.1);
}

.btn-small {
  padding: 6px 12px;
  font-size: 12px;
}

.btn-secondary {
  background-color: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn-secondary:hover:not(:disabled) {
  border-color: #4fc3f7;
  color: #4fc3f7;
}

.btn-icon {
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 6px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s ease;
  color: #8899aa;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #e0e6ed;
}

.btn-icon.danger:hover {
  background: rgba(239, 83, 80, 0.1);
  color: #ef5350;
}

/* Context Window Config Styles */
.context-window-config {
  background: rgba(26, 31, 46, 0.5);
  border: 1px dashed #2a2f3e;
  border-radius: 8px;
  padding: 16px;
  margin: 16px 0;
}

.input-with-presets {
  display: flex;
  gap: 8px;
  align-items: center;
}

.input-with-presets .input-field {
  flex: 1;
}

.preset-buttons {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.btn-xs {
  padding: 4px 8px;
  font-size: 12px;
  background: rgba(79, 195, 247, 0.1);
  border: 1px solid #2a2f3e;
  border-radius: 4px;
  color: #4fc3f7;
  cursor: pointer;
  transition: all 0.15s ease;
}

.btn-xs:hover {
  background: rgba(79, 195, 247, 0.2);
  border-color: #4fc3f7;
}

/* Section Header Actions */
.section-header-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

.legacy-badge {
  display: inline-block;
  font-size: 11px;
  font-weight: 500;
  padding: 1px 6px;
  border-radius: 3px;
  background: rgba(255, 167, 38, 0.15);
  color: #ffa726;
  vertical-align: middle;
  margin-left: 6px;
}

.btn-text-link {
  background: none;
  border: none;
  color: #8899aa;
  font-size: 12px;
  cursor: pointer;
  padding: 4px 8px;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.btn-text-link:hover {
  color: #bcc8d4;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 4px;
}

@media (max-width: 768px) {
  .key-input-row {
    flex-direction: column;
    align-items: stretch;
  }
}

/* JSON Editor Dialog */
.json-editor-dialog {
  max-width: 720px;
  width: 100%;
  padding: 0;
}

.json-editor-dialog h2 {
  margin: 0;
  padding: 20px;
  border-bottom: 1px solid #2a2f3e;
  color: #e0e6ed;
  font-size: 18px;
}

.json-editor-body {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  overflow: hidden;
}

.json-textarea {
  width: 100%;
  min-height: 400px;
  max-height: 55vh;
  background: #0a0d14;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #c9e0f0;
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  font-size: 13px;
  line-height: 1.6;
  padding: 12px;
  resize: vertical;
  outline: none;
  box-sizing: border-box;
  transition: border-color 0.15s ease;
  tab-size: 2;
}

.json-textarea:focus {
  border-color: #4fc3f7;
}

.json-status {
  font-size: 13px;
  padding: 4px 0;
  min-height: 20px;
  color: #5a6a7a;
}

.json-status.success {
  color: #66bb6a;
}

.json-status.error {
  color: #ef5350;
}

/* OpenCode Config JSON textarea in preset dialog */
.json-config-textarea {
  min-height: 100px;
  resize: vertical;
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  font-size: 13px;
  line-height: 1.5;
  tab-size: 2;
}

.json-config-textarea:focus {
  border-color: #4fc3f7;
}

.json-config-status {
  font-size: 12px;
  margin-top: 4px;
  min-height: 18px;
  color: #5a6a7a;
}

.json-config-status.success {
  color: #66bb6a;
}

.json-config-status.error {
  color: #ef5350;
}

/* OpenCode Structured GUI Styles */
.opencode-gui-section {
  margin-top: 16px;
  border: 1px solid #2a3040;
  border-radius: 8px;
  overflow: hidden;
}

.opencode-gui-header {
  padding: 14px 18px;
  background: rgba(79, 195, 247, 0.06);
  border-bottom: 1px solid #2a3040;
  display: flex;
  align-items: baseline;
  gap: 12px;
}

.opencode-gui-title {
  font-weight: 600;
  font-size: 15px;
  color: #e0e6ed;
}

.opencode-gui-hint {
  font-size: 12px;
  color: #5a6a7a;
}

.oc-collapsible {
  border-bottom: 1px solid #2a3040;
}

.oc-collapsible:last-child {
  border-bottom: none;
}

.oc-collapsible-header {
  padding: 12px 18px;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 500;
  color: #8899aa;
  transition: background-color 0.12s ease, color 0.12s ease;
  user-select: none;
}

.oc-collapsible-header:hover {
  background: rgba(79, 195, 247, 0.04);
  color: #c0d0e0;
}

.oc-collapse-icon {
  font-size: 10px;
  color: #4fc3f7;
  width: 14px;
  text-align: center;
}

.oc-count-badge {
  font-size: 11px;
  font-weight: 600;
  background: rgba(79, 195, 247, 0.15);
  color: #4fc3f7;
  padding: 1px 7px;
  border-radius: 10px;
  margin-left: 4px;
}

.oc-collapsible-body {
  padding: 4px 18px 16px;
}

.oc-list-item {
  background: rgba(15, 18, 25, 0.5);
  border: 1px solid #222838;
  border-radius: 6px;
  padding: 12px;
  margin-bottom: 10px;
}

.oc-list-item-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}

.oc-list-item-name {
  font-weight: 600;
  font-size: 13px;
  color: #c0d0e0;
  font-family: 'Consolas', 'Monaco', monospace;
}

.oc-kv-row {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}

.oc-kv-key {
  flex: 2;
}

.oc-kv-value {
  flex: 1;
}

.oc-config-summary {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  line-height: 1.4;
  word-break: break-all;
}
</style>
