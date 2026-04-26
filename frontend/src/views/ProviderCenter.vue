<template>
  <div class="pc-layout">
    <!-- Left Sidebar -->
    <div class="pc-sidebar">
      <h1 class="pc-page-title">Provider Center</h1>
      <nav class="pc-nav">
        <button
          v-for="tab in navTabs"
          :key="tab.id"
          class="pc-nav-tab"
          :class="{ active: activeSection === tab.id }"
          @click="activeSection = tab.id"
        >
          <span class="pc-nav-icon">{{ tab.icon }}</span>
          <span class="pc-nav-label">{{ tab.label }}</span>
        </button>
      </nav>
    </div>

    <!-- Right Content -->
    <div class="pc-content">

      <!-- ============ PROVIDERS TAB ============ -->
      <template v-if="activeSection === 'providers'">
        <!-- List Mode -->
        <template v-if="!selectedProviderName">
          <div class="pc-section-header">
            <div>
              <h2>服务提供商</h2>
              <p>管理 AI 服务提供商的连接配置。</p>
            </div>
            <div class="pc-header-actions">
              <button class="btn secondary small" @click="handleExportConfig" :disabled="loading || exporting">
                {{ exporting ? '导出中...' : '导出配置' }}
              </button>
              <button class="btn secondary small" @click="handleImportConfig" :disabled="loading">JSON 导入</button>
              <button class="btn primary small" @click="openAddProviderDialog">添加提供商</button>
            </div>
          </div>

          <div class="provider-filter-tabs">
            <button class="filter-tab" :class="{ active: filterType === 'all' }" @click="filterType = 'all'">全部</button>
            <button class="filter-tab" :class="{ active: filterType === 'anthropic' }" @click="filterType = 'anthropic'">Anthropic</button>
            <button class="filter-tab" :class="{ active: filterType === 'openai' }" @click="filterType = 'openai'">OpenAI</button>
          </div>

          <div class="provider-grid">
            <div class="card provider-card" v-for="(pInfo, pName) in filteredProviders" :key="pName" @click="selectedProviderName = String(pName)">
              <div class="card-header">
                <div class="provider-title-group">
                  <h2 class="provider-name">{{ pName }}</h2>
                  <div class="key-status-indicator">
                    <span :class="['status-dot', apiKeyStatus[String(pName)] ? 'configured' : 'unconfigured']"></span>
                    <span class="status-text">{{ apiKeyStatus[String(pName)] ? '已配置密钥' : '未配置密钥' }}</span>
                  </div>
                </div>
                <button class="btn-icon danger" @click.stop="handleDeleteProvider(String(pName))" title="删除" :disabled="loading">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                </button>
              </div>
              <div class="card-body">
                <div class="info-row">
                  <span class="label">Base URL:</span>
                  <span class="value truncate" :title="pInfo.base_url">{{ pInfo.base_url || '-' }}</span>
                </div>
                <div class="info-row">
                  <span class="label">默认模型:</span>
                  <span class="value truncate" :title="pInfo.default_model">{{ pInfo.default_model || '-' }}</span>
                </div>
                <div class="badge-row">
                  <span class="badge format-anthropic" v-if="hasAnthropicFormat(pInfo as any)">A</span>
                  <span class="badge format-openai" v-if="hasOpenAIFormat(pInfo as any)">O</span>
                  <span class="badge" v-if="!hasAnthropicFormat(pInfo as any) && !hasOpenAIFormat(pInfo as any)">{{ ((pInfo as any).type || 'anthropic').toUpperCase() }}</span>
                </div>
              </div>
            </div>

            <div v-if="Object.keys(filteredProviders).length === 0" class="empty-state">
              <p class="muted" v-if="Object.keys(providers).length === 0">暂无服务提供商，请点击右上角添加</p>
              <p class="muted" v-else>当前筛选条件下无匹配的服务提供商</p>
            </div>
          </div>

          <!-- Add Provider Dialog -->
          <div class="dialog-overlay" v-if="showAddDialog" @click.self="resetAddProviderForm">
            <div class="dialog card">
              <h2>添加提供商</h2>
              <div class="form-group">
                <label>支持格式</label>
                <div class="type-selector capability-selector">
                  <label class="type-btn capability-toggle" :class="{ active: newProviderSupportsAnthropic }">
                    <input type="checkbox" v-model="newProviderSupportsAnthropic" />
                    <span>Anthropic</span>
                  </label>
                  <label class="type-btn capability-toggle" :class="{ active: newProviderSupportsOpenAI }">
                    <input type="checkbox" v-model="newProviderSupportsOpenAI" />
                    <span>OpenAI</span>
                  </label>
                </div>
                <p class="tp-compat-warning" v-if="!newProviderSupportsAnthropic && !newProviderSupportsOpenAI">至少启用一种 Provider 格式。</p>
              </div>
              <div class="form-group">
                <label>名称 (唯一标识)</label>
                <input type="text" v-model="newProviderName" class="input-field" placeholder="例如: anthropic, openai" />
              </div>
              <div class="form-group" v-if="newProviderSupportsAnthropic">
                <label>Anthropic Base URL</label>
                <input type="text" v-model="newProviderAnthropicBaseUrl" class="input-field" placeholder="https://api.anthropic.com" />
              </div>
              <div class="form-group" v-if="newProviderSupportsOpenAI">
                <label>OpenAI Base URL</label>
                <input type="text" v-model="newProviderOpenAIBaseUrl" class="input-field" placeholder="https://api.openai.com/v1" />
              </div>
              <div class="form-group">
                <label>默认模型</label>
                <input type="text" v-model="newProviderModel" class="input-field" :placeholder="newProviderSupportsOpenAI && !newProviderSupportsAnthropic ? 'o3' : 'claude-3-7-sonnet-20250219'" />
              </div>
              <div class="dialog-actions">
                <button class="btn secondary" @click="resetAddProviderForm" :disabled="loading">取消</button>
                <button class="btn primary" @click="handleAddProvider" :disabled="!newProviderName || (!newProviderSupportsAnthropic && !newProviderSupportsOpenAI) || loading">
                  {{ loading ? '处理中...' : '保存' }}
                </button>
              </div>
            </div>
          </div>
        </template>

        <!-- Detail Mode -->
        <template v-else>
          <ProviderDetail
            :provider-name="selectedProviderName"
            :show-breadcrumb="true"
            @back="selectedProviderName = ''"
            @saved="loadProviders"
          />
        </template>
      </template>

      <!-- ============ CLAUDE CODE TAB ============ -->
      <div v-if="activeSection === 'claude-code'" class="pc-section">
        <div class="pc-section-header">
          <div>
            <h2>Claude Code 终端预设</h2>
            <p>管理 Claude Code 启动预设配置。</p>
          </div>
          <button class="btn primary small" @click="tpOpenAdd('claude_code')">+ 添加预设</button>
        </div>

        <div class="provider-filter-tabs" v-if="claudeCodeProviderNames.length > 0">
          <button class="filter-tab" :class="{ active: claudeCodeFilterProvider === '' }" @click="claudeCodeFilterProvider = ''">全部</button>
          <button
            v-for="name in claudeCodeProviderNames"
            :key="name"
            class="filter-tab"
            :class="{ active: claudeCodeFilterProvider === name }"
            @click="claudeCodeFilterProvider = name"
          >{{ name }}</button>
        </div>

        <div class="pc-preset-card-grid" v-if="filteredClaudeCodePresets.length > 0">
          <div class="card tp-preset-card" v-for="p in filteredClaudeCodePresets" :key="p.name">
            <div class="tp-preset-header">
              <div>
                <strong class="tp-preset-name">{{ p.label || p.name }}</strong>
                <span class="tp-preset-provider">{{ p.provider }}</span>
              </div>
              <div class="tp-preset-actions">
                <button class="btn-icon" @click="tpOpenEdit('claude_code', p)" title="编辑">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
                </button>
                <button class="btn-icon danger" @click="tpHandleDelete('claude_code', p.name)" title="删除">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                </button>
              </div>
            </div>
            <div class="tp-preset-body">
              <span class="param-badge" v-if="p.model">Model: {{ p.model }}</span>
              <span class="param-badge" v-if="p.parameters?.temperature !== undefined">Temp: {{ p.parameters.temperature }}</span>
              <span class="param-badge" v-if="p.parameters?.top_p !== undefined">Top P: {{ p.parameters.top_p }}</span>
              <span class="param-badge" v-if="p.parameters?.max_tokens">Max Tokens: {{ p.parameters.max_tokens }}</span>
              <span class="param-badge" v-if="p.parameters?.stream !== undefined">{{ p.parameters.stream ? 'Stream' : 'No Stream' }}</span>
              <span class="param-badge" v-if="p.parameters?.thinking?.type === 'enabled'">Thinking{{ p.parameters.thinking.budgetTokens ? ' (' + p.parameters.thinking.budgetTokens + ')' : '' }}</span>
              <span class="param-badge" v-if="p.parameters?.context_window?.model_context_window">Window: {{ p.parameters.context_window.model_context_window }}</span>
            </div>
          </div>
        </div>
        <div class="empty-state" v-else>
          <span v-if="claudeCodeFilterProvider">该提供商下暂无预设</span>
          <span v-else>暂无 Claude Code 预设。点击"+ 添加预设"创建。</span>
        </div>
      </div>

      <!-- ============ OPENCODE TAB ============ -->
      <div v-if="activeSection === 'opencode'" class="pc-section">
        <!-- OpenCode Preset Manager -->
        <div class="pc-section-header">
          <div>
            <h2>OpenCode 预设</h2>
            <p>管理 OpenCode 启动预设。每个预设包含一份完整的 opencode.json 配置及 provider 绑定。</p>
          </div>
          <button class="btn primary small" @click="ocPresetOpenAdd">+ 添加预设</button>
        </div>

        <div class="oc-preset-search" v-if="ocPresetList.length > 3">
          <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>
          <input type="text" v-model="ocPresetSearchQuery" class="input-field oc-preset-search-input" placeholder="搜索预设名称或描述..." />
        </div>

        <div class="tp-presets-list" v-if="ocPresetList.length > 0">
          <div class="card oc-preset-manage-card" v-for="p in filteredOcPresetList" :key="p.key">
            <div class="oc-preset-manage-header">
              <div class="oc-preset-manage-info">
                <strong class="oc-preset-manage-name">{{ p.name || p.key }}</strong>
                <span class="oc-preset-manage-desc" v-if="p.description">{{ p.description }}</span>
              </div>
              <div class="oc-preset-manage-meta">
                <span class="oc-preset-manage-badge" v-if="p.bindingCount > 0">{{ p.bindingCount }} 绑定</span>
                <div class="tp-preset-actions">
                  <button class="btn-icon" @click="ocPresetOpenEdit(p)" title="编辑">
                    <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
                  </button>
                  <button class="btn-icon danger" @click="ocPresetHandleDelete(p.key)" title="删除">
                    <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                  </button>
                </div>
              </div>
            </div>
          </div>
          <div v-if="filteredOcPresetList.length === 0 && ocPresetSearchQuery" class="oc-preset-search-empty">
            未找到匹配的预设
          </div>
        </div>
        <div class="empty-state" v-else>
          <span>暂无 OpenCode 预设。点击"+ 添加预设"创建。</span>
        </div>

        <!-- OpenCode Preset Dialog -->
        <div class="dialog-overlay" v-if="ocPresetShowDialog" @click.self="ocPresetShowDialog = false">
          <div class="dialog card" style="max-width: 640px;">
            <h2>{{ ocPresetIsEditing ? '编辑' : '添加' }} OpenCode 预设</h2>
            <div class="dialog-scroll-area">
              <div class="form-group" v-if="!ocPresetIsEditing">
                <label>预设 Key（唯一标识）</label>
                <input type="text" v-model="ocPresetEditing.key" class="input-field" placeholder="例如: my-preset" />
              </div>
              <div class="form-group">
                <label>名称</label>
                <input type="text" v-model="ocPresetEditing.name" class="input-field" placeholder="预设显示名称" />
              </div>
              <div class="form-group">
                <label>描述</label>
                <input type="text" v-model="ocPresetEditing.description" class="input-field" placeholder="可选描述" />
              </div>

              <div class="form-group">
                <label>opencode.json 配置（JSON）</label>
                <textarea
                  class="input-field monospace"
                  v-model="ocPresetConfigJson"
                  rows="10"
                  spellcheck="false"
                  placeholder='{ "model": "openai/gpt-4o" }'
                ></textarea>
                <span class="tp-compat-warning" v-if="ocPresetConfigError">{{ ocPresetConfigError }}</span>
              </div>

              <div class="form-group">
                <label>Provider 绑定</label>
                <p style="font-size: 12px; color: #5a6a7a; margin: 0 0 8px;">将 opencode.json 中的 provider ID 映射到本地已配置的 Provider。</p>
                <div v-for="(binding, idx) in ocPresetEditing.bindings" :key="idx" class="oc-preset-binding-row">
                  <div class="oc-kv-row">
                    <input type="text" v-model="binding.providerId" class="input-field oc-kv-key" placeholder="OpenCode provider ID" />
                    <select v-model="binding.localProvider" class="input-field oc-kv-value">
                      <option value="">（无映射）</option>
                      <option v-for="(_, pName) in providers" :key="pName" :value="pName">{{ pName }}</option>
                    </select>
                    <button class="oc-remove-btn" @click="ocPresetEditing.bindings.splice(idx, 1)" title="删除">&#10005;</button>
                  </div>
                  <div class="oc-kv-row" style="margin-top: 4px;">
                    <select v-model="binding.format" class="input-field" style="width: 120px; flex: none;">
                      <option value="">自动</option>
                      <option value="openai">OpenAI</option>
                      <option value="anthropic">Anthropic</option>
                    </select>
                    <div class="tp-preset-body" style="flex: 1; gap: 4px;">
                      <label style="font-size: 12px; color: #5a6a7a; display: inline-flex; align-items: center; gap: 4px; cursor: pointer;">
                        <input type="checkbox" v-model="binding.injectApiKey" /> apiKey
                      </label>
                      <label style="font-size: 12px; color: #5a6a7a; display: inline-flex; align-items: center; gap: 4px; cursor: pointer;">
                        <input type="checkbox" v-model="binding.injectBaseURL" /> baseURL
                      </label>
                      <label style="font-size: 12px; color: #5a6a7a; display: inline-flex; align-items: center; gap: 4px; cursor: pointer;">
                        <input type="checkbox" v-model="binding.injectOrganization" /> organization
                      </label>
                    </div>
                  </div>
                </div>
                <button class="btn small" @click="ocPresetAddBinding">+ 添加绑定</button>
              </div>
            </div>
            <div class="dialog-actions">
              <button class="btn secondary" @click="ocPresetShowDialog = false">取消</button>
              <button class="btn primary" @click="ocPresetHandleSave" :disabled="!ocPresetEditing.key || !ocPresetEditing.name || !!ocPresetConfigError">保存</button>
            </div>
          </div>
        </div>

        <!-- Divider -->
        <div class="pc-section-divider"></div>

        <!-- OpenCode Global Config -->
        <div class="pc-section-header">
          <div>
            <h2>OpenCode 全局配置</h2>
            <p>编辑全局 opencode.json 配置文件。修改后保存立即生效。</p>
          </div>
          <div class="oc-mode-switch">
            <button class="oc-mode-btn" :class="{ active: ocEditMode === 'visual' }" @click="ocSwitchToVisual">可视化</button>
            <button class="oc-mode-btn" :class="{ active: ocEditMode === 'json' }" @click="ocSwitchToJson">JSON</button>
          </div>
        </div>

        <div class="form-group" v-if="ocConfigPath">
          <label>配置文件路径</label>
          <div class="inline-input-group">
            <input type="text" class="input-field monospace flex-1" :value="ocConfigPath" readonly />
            <button class="btn small" @click="copyConfigPath">复制路径</button>
          </div>
        </div>

        <div class="oc-status-bar">
          <span v-if="ocHasUnsavedChanges" class="opencode-unsaved-badge">未保存的更改</span>
          <span class="oc-validation" :class="ocValidationClass">{{ ocValidationText }}</span>
          <span v-if="ocSwitchBlocked" class="oc-switch-warning">JSON 非法，无法切换模式</span>
        </div>

        <div class="group-separator"></div>

        <!-- VISUAL MODE -->
        <div v-if="ocEditMode === 'visual'" class="oc-visual-mode">
          <div v-if="ocHasSubJsonErrors" class="oc-sub-error-banner">
            <span class="oc-sub-error-icon">!</span>
            <div class="oc-sub-error-content">
              <div class="oc-sub-error-title">存在字段解析错误，保存已被阻止</div>
              <div v-for="(msg, field) in ocSubJsonErrors" :key="field" class="oc-sub-error-item">
                <span class="oc-sub-error-field">{{ field }}</span>: {{ msg }}
              </div>
            </div>
          </div>

          <!-- Model -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('model')">
              <span class="oc-collapse-icon">{{ ocSections.model ? '&#9660;' : '&#9654;' }}</span>
              <span>Model</span>
            </div>
            <div class="oc-section-body" v-if="ocSections.model">
              <div class="form-group">
                <label>默认模型</label>
                <input type="text" v-model="ocGui.modelValue" class="input-field monospace" placeholder="anthropic/claude-sonnet-4-6" @input="ocGuiToRaw" />
              </div>
            </div>
          </div>

          <!-- Provider -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('provider')">
              <span class="oc-collapse-icon">{{ ocSections.provider ? '&#9660;' : '&#9654;' }}</span>
              <span>Provider <span class="oc-count-badge" v-if="ocGui.providers.length">{{ ocGui.providers.length }}</span></span>
            </div>
            <div class="oc-section-body" v-if="ocSections.provider">
              <div v-for="(prov, idx) in ocGui.providers" :key="idx" class="oc-card">
                <div class="oc-card-header">
                  <span class="oc-card-name">{{ prov.name || '(unnamed)' }}</span>
                  <button class="oc-remove-btn" @click="ocRemoveProvider(idx)" title="删除">&#10005;</button>
                </div>
                <div class="form-group">
                  <label>Provider ID</label>
                  <input type="text" v-model="prov.name" class="input-field" placeholder="anthropic, openai..." @input="ocGuiToRaw" />
                </div>
                <div class="form-row">
                  <div class="form-group flex-1">
                    <label>API Key</label>
                    <input type="password" v-model="prov.apiKey" class="input-field monospace" placeholder="sk-..." @input="ocGuiToRaw" />
                  </div>
                  <div class="form-group flex-1">
                    <label>Base URL</label>
                    <input type="text" v-model="prov.baseURL" class="input-field monospace" placeholder="https://api.anthropic.com" @input="ocGuiToRaw" />
                  </div>
                </div>
                <div class="form-group">
                  <label>Models</label>
                  <div v-for="(model, midx) in prov.models" :key="midx" class="oc-sub-card">
                    <div class="oc-card-header">
                      <span class="oc-card-name">{{ model.key || '(unnamed model)' }}</span>
                      <button class="oc-remove-btn" @click="prov.models.splice(midx, 1); ocGuiToRaw()" title="删除">&#10005;</button>
                    </div>
                    <div class="form-row">
                      <div class="form-group flex-1">
                        <label>Model Key</label>
                        <input type="text" v-model="model.key" class="input-field monospace" placeholder="glm-5-turbo" @input="ocGuiToRaw" />
                      </div>
                      <div class="form-group flex-1">
                        <label>显示名</label>
                        <input type="text" v-model="model.name" class="input-field" placeholder="GLM 5 Turbo" @input="ocGuiToRaw" />
                      </div>
                    </div>
                    <div class="form-group">
                      <label>Variants（逗号分隔）</label>
                      <input type="text" :value="model.variants.join(', ')" class="input-field monospace" placeholder="medium, high, max" @input="ocUpdateProviderModelVariantsFromEvent(model, $event)" />
                    </div>
                    <div class="form-group">
                      <label>Options JSON</label>
                      <textarea v-model="model.optionsRaw" class="input-field monospace" rows="4" placeholder='{"reasoning":true}' @input="ocGuiToRaw"></textarea>
                    </div>
                  </div>
                  <button class="btn small" @click="ocAddProviderModel(prov)">+ 添加 Model</button>
                </div>
              </div>
              <button class="btn small" @click="ocAddProvider">+ 添加 Provider</button>
            </div>
          </div>

          <!-- MCP -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('mcp')">
              <span class="oc-collapse-icon">{{ ocSections.mcp ? '&#9660;' : '&#9654;' }}</span>
              <span>MCP Servers <span class="oc-count-badge" v-if="ocGui.mcpServers.length">{{ ocGui.mcpServers.length }}</span></span>
            </div>
            <div class="oc-section-body" v-if="ocSections.mcp">
              <div v-for="(mcp, idx) in ocGui.mcpServers" :key="idx" class="oc-card">
                <div class="oc-card-header">
                  <span class="oc-card-name">{{ mcp.name || '(unnamed)' }}</span>
                  <button class="oc-remove-btn" @click="ocRemoveMcp(idx)" title="删除">&#10005;</button>
                </div>
                <div class="form-row">
                  <div class="form-group flex-1">
                    <label>名称</label>
                    <input type="text" v-model="mcp.name" class="input-field" @input="ocGuiToRaw" />
                  </div>
                  <div class="form-group" style="width: 140px;">
                    <label>Type</label>
                    <select v-model="mcp.type" class="input-field" @change="ocGuiToRaw">
                      <option value="remote">remote</option>
                      <option value="local">local</option>
                    </select>
                  </div>
                </div>
                <div class="form-group" v-if="mcp.type === 'remote'">
                  <label>URL</label>
                  <input type="text" v-model="mcp.url" class="input-field" @input="ocGuiToRaw" />
                </div>
                <div class="form-group" v-if="mcp.type === 'local'">
                  <label>Command</label>
                  <div v-for="(arg, aidx) in mcp.commandArgs" :key="aidx" class="oc-kv-row">
                    <input type="text" v-model="mcp.commandArgs[aidx]" class="input-field" :placeholder="aidx === 0 ? '可执行文件' : '参数'" @input="ocGuiToRaw" />
                    <button class="oc-remove-btn" @click="mcp.commandArgs.splice(aidx, 1); ocGuiToRaw()" title="删除">&#10005;</button>
                  </div>
                  <button class="btn small" @click="mcp.commandArgs.push(''); ocGuiToRaw()">+ 添加参数</button>
                </div>
                <div class="form-group">
                  <label>Headers</label>
                  <div v-for="(header, hidx) in mcp.headers" :key="`header-${hidx}`" class="oc-kv-row">
                    <input type="text" v-model="header.key" class="input-field oc-kv-key" placeholder="Header 名称" @input="ocGuiToRaw" />
                    <input type="text" v-model="header.value" class="input-field oc-kv-value monospace" placeholder="Header 值" @input="ocGuiToRaw" />
                    <button class="oc-remove-btn" @click="mcp.headers.splice(hidx, 1); ocGuiToRaw()" title="删除">&#10005;</button>
                  </div>
                  <button class="btn small" @click="mcp.headers.push({ key: '', value: '' }); ocGuiToRaw()">+ 添加 Header</button>
                </div>
                <div class="form-group">
                  <label>Environment</label>
                  <div v-for="(env, eidx) in mcp.environment" :key="`env-${eidx}`" class="oc-kv-row">
                    <input type="text" v-model="env.key" class="input-field oc-kv-key" placeholder="环境变量名" @input="ocGuiToRaw" />
                    <input type="text" v-model="env.value" class="input-field oc-kv-value monospace" placeholder="环境变量值" @input="ocGuiToRaw" />
                    <button class="oc-remove-btn" @click="mcp.environment.splice(eidx, 1); ocGuiToRaw()" title="删除">&#10005;</button>
                  </div>
                  <button class="btn small" @click="mcp.environment.push({ key: '', value: '' }); ocGuiToRaw()">+ 添加环境变量</button>
                </div>
                <div class="form-group">
                  <label style="display: inline-flex; align-items: center; gap: 8px; cursor: pointer;">
                    <input type="checkbox" v-model="mcp.oauth" @change="ocGuiToRaw" />
                    <span>启用 OAuth</span>
                  </label>
                </div>
              </div>
              <button class="btn small" @click="ocAddMcp">+ 添加 MCP Server</button>
            </div>
          </div>

          <!-- Agent -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('agent')">
              <span class="oc-collapse-icon">{{ ocSections.agent ? '&#9660;' : '&#9654;' }}</span>
              <span>Agent <span class="oc-count-badge" v-if="ocGui.agents.length">{{ ocGui.agents.length }}</span></span>
            </div>
            <div class="oc-section-body" v-if="ocSections.agent">
              <div v-for="(agent, idx) in ocGui.agents" :key="idx" class="oc-card">
                <div class="oc-card-header">
                  <span class="oc-card-name" :style="{ color: agent.color || undefined }">{{ agent.name || '(unnamed)' }}</span>
                  <button class="oc-remove-btn" @click="ocRemoveAgent(idx)" title="删除">&#10005;</button>
                </div>
                <div class="form-row">
                  <div class="form-group flex-1"><label>名称</label><input type="text" v-model="agent.name" class="input-field" @input="ocGuiToRaw" /></div>
                  <div class="form-group" style="width: 160px;"><label>Mode</label><select v-model="agent.mode" class="input-field" @change="ocGuiToRaw"><option value="primary">primary</option><option value="subagent">subagent</option></select></div>
                </div>
                <div class="form-row">
                  <div class="form-group flex-1"><label>Model</label><input type="text" v-model="agent.model" class="input-field monospace" @input="ocGuiToRaw" /></div>
                  <div class="form-group" style="width: 160px;"><label>Variant</label><select v-model="agent.variant" class="input-field" @change="ocGuiToRaw"><option value="">默认</option><option value="low">low</option><option value="medium">medium</option><option value="high">high</option><option value="xhigh">xhigh</option><option value="max">max</option></select></div>
                </div>
                <div class="form-group">
                  <label>Color</label>
                  <div class="oc-color-row">
                    <input type="text" v-model="agent.color" class="input-field monospace flex-1" placeholder="#FF69B4" @input="ocGuiToRaw" />
                    <input type="color" class="oc-color-picker" :value="ocAgentPickerColor(agent.color)" @input="ocSetAgentColorFromPickerEvent(agent, $event)" />
                    <span class="oc-color-preview" :class="{ invalid: !isValidHexColor(agent.color) }" :style="isValidHexColor(agent.color) ? { backgroundColor: normalizeHexColor(agent.color) } : undefined"></span>
                  </div>
                </div>
                <div class="form-group"><label>Prompt</label><textarea v-model="agent.prompt" class="input-field" rows="2" @input="ocGuiToRaw"></textarea></div>
              </div>
              <button class="btn small" @click="ocAddAgent">+ 添加 Agent</button>
            </div>
          </div>

          <!-- Permission -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('permission')">
              <span class="oc-collapse-icon">{{ ocSections.permission ? '&#9660;' : '&#9654;' }}</span>
              <span>Permission <span class="oc-count-badge" v-if="ocGui.permissions.length">{{ ocGui.permissions.length }}</span></span>
            </div>
            <div class="oc-section-body" v-if="ocSections.permission">
              <div v-for="(perm, idx) in ocGui.permissions" :key="idx" class="oc-kv-row">
                <input type="text" v-model="perm.key" class="input-field oc-kv-key" placeholder="tool 名称" @input="ocGuiToRaw" />
                <select v-model="perm.value" class="input-field oc-kv-value" @change="ocGuiToRaw">
                  <option value="allow">allow</option><option value="deny">deny</option><option value="ask">ask</option>
                </select>
                <button class="oc-remove-btn" @click="ocRemovePermission(idx)" title="删除">&#10005;</button>
              </div>
              <button class="btn small" @click="ocAddPermission">+ 添加权限</button>
            </div>
          </div>

          <!-- Instructions -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('instructions')">
              <span class="oc-collapse-icon">{{ ocSections.instructions ? '&#9660;' : '&#9654;' }}</span>
              <span>Instructions <span class="oc-count-badge" v-if="ocGui.instructions.length">{{ ocGui.instructions.length }}</span></span>
            </div>
            <div class="oc-section-body" v-if="ocSections.instructions">
              <div v-for="(instr, idx) in ocGui.instructions" :key="idx" class="oc-kv-row">
                <input type="text" v-model="ocGui.instructions[idx]" class="input-field" placeholder="resources/path/to/file.md" @input="ocGuiToRaw" />
                <button class="oc-remove-btn" @click="ocRemoveInstruction(idx)" title="删除">&#10005;</button>
              </div>
              <button class="btn small" @click="ocAddInstruction">+ 添加 Instruction</button>
            </div>
          </div>

          <!-- Plugin -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('plugin')">
              <span class="oc-collapse-icon">{{ ocSections.plugin ? '&#9660;' : '&#9654;' }}</span>
              <span>Plugin <span class="oc-count-badge" v-if="ocGui.plugins.length">{{ ocGui.plugins.length }}</span></span>
            </div>
            <div class="oc-section-body" v-if="ocSections.plugin">
              <div v-for="(plug, idx) in ocGui.plugins" :key="idx" class="oc-kv-row">
                <input type="text" v-model="ocGui.plugins[idx]" class="input-field" @input="ocGuiToRaw" />
                <button class="oc-remove-btn" @click="ocRemovePlugin(idx)" title="删除">&#10005;</button>
              </div>
              <button class="btn small" @click="ocAddPlugin">+ 添加 Plugin</button>
            </div>
          </div>

          <!-- Experimental -->
          <div class="oc-section">
            <div class="oc-section-header" @click="ocToggleSection('experimental')">
              <span class="oc-collapse-icon">{{ ocSections.experimental ? '&#9660;' : '&#9654;' }}</span>
              <span>Experimental <span class="oc-count-badge" v-if="ocGui.experimentalKvs.length">{{ ocGui.experimentalKvs.length }}</span></span>
            </div>
            <div class="oc-section-body" v-if="ocSections.experimental">
              <div v-for="(kv, idx) in ocGui.experimentalKvs" :key="idx" class="oc-kv-row">
                <input type="text" v-model="kv.key" class="input-field" style="width:140px; flex:none;" placeholder="键名" @input="ocGuiToRaw" />
                <input type="text" v-model="kv.valueRaw" class="input-field monospace" placeholder='JSON 值' @input="ocGuiToRaw" />
                <button class="oc-remove-btn" @click="ocRemoveExperimentalKv(idx)" title="删除">&#10005;</button>
              </div>
              <button class="btn small" @click="ocAddExperimentalKv">+ 添加 Experimental 项</button>
            </div>
          </div>
        </div>

        <!-- JSON MODE -->
        <div v-if="ocEditMode === 'json'" class="setting-group">
          <textarea class="input-field monospace opencode-editor" v-model="ocEditorContent" spellcheck="false" placeholder="{ }"></textarea>
          <div v-if="ocValidationError && ocValidationError !== ''" class="oc-error-detail">{{ ocValidationError }}</div>
        </div>

        <!-- OpenCode Actions -->
        <div class="opencode-actions">
          <button class="btn small" @click="ocReload" :disabled="ocReloading">{{ ocReloading ? '加载中...' : '重新加载' }}</button>
          <button class="btn small" @click="ocFormat" :disabled="!ocIsParseableJson">格式化</button>
          <button class="btn small danger" @click="ocRevert" :disabled="!ocHasUnsavedChanges || ocReverting">{{ ocReverting ? '恢复中...' : '恢复到磁盘' }}</button>
          <div class="opencode-actions-spacer"></div>
          <button class="btn primary" @click="ocSave" :disabled="!ocCanSave || ocSaving">{{ ocSaving ? '保存中...' : '保存' }}</button>
        </div>
      </div>

      <!-- ============ CODEX TAB ============ -->
      <div v-if="activeSection === 'codex'" class="pc-section">
        <div class="pc-section-header">
          <div>
            <h2>Codex 终端预设</h2>
            <p>管理 Codex 启动预设配置。</p>
          </div>
          <button class="btn primary small" @click="tpOpenAdd('codex')">+ 添加预设</button>
        </div>

        <div class="provider-filter-tabs" v-if="codexProviderNames.length > 0">
          <button class="filter-tab" :class="{ active: codexFilterProvider === '' }" @click="codexFilterProvider = ''">全部</button>
          <button
            v-for="name in codexProviderNames"
            :key="name"
            class="filter-tab"
            :class="{ active: codexFilterProvider === name }"
            @click="codexFilterProvider = name"
          >{{ name }}</button>
        </div>

        <div class="pc-preset-card-grid" v-if="filteredCodexPresets.length > 0">
          <div class="card tp-preset-card" v-for="p in filteredCodexPresets" :key="p.name">
            <div class="tp-preset-header">
              <div>
                <strong class="tp-preset-name">{{ p.label || p.name }}</strong>
                <span class="tp-preset-provider">{{ p.provider }}</span>
              </div>
              <div class="tp-preset-actions">
                <button class="btn-icon" @click="tpOpenEdit('codex', p)" title="编辑">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
                </button>
                <button class="btn-icon danger" @click="tpHandleDelete('codex', p.name)" title="删除">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                </button>
              </div>
            </div>
            <div class="tp-preset-body">
              <span class="param-badge" v-if="p.model">Model: {{ p.model }}</span>
              <span class="param-badge" v-if="p.parameters?.temperature !== undefined">Temp: {{ p.parameters.temperature }}</span>
              <span class="param-badge" v-if="p.parameters?.top_p !== undefined">Top P: {{ p.parameters.top_p }}</span>
              <span class="param-badge" v-if="p.parameters?.max_tokens">Max Tokens: {{ p.parameters.max_tokens }}</span>
              <span class="param-badge" v-if="p.parameters?.stream !== undefined">{{ p.parameters.stream ? 'Stream' : 'No Stream' }}</span>
              <span class="param-badge" v-if="p.parameters?.context_window?.model_context_window">Window: {{ p.parameters.context_window.model_context_window }}</span>
              <span class="param-badge" v-if="p.parameters?.context_window?.model_auto_compact_token_limit">Compact@: {{ p.parameters.context_window.model_auto_compact_token_limit }}</span>
            </div>
          </div>
        </div>
        <div class="empty-state" v-else>
          <span v-if="codexFilterProvider">该提供商下暂无预设</span>
          <span v-else>暂无 Codex 预设。点击"+ 添加预设"创建。</span>
        </div>
      </div>

    </div>

    <!-- ============ TERMINAL PRESET DIALOG (shared) ============ -->
    <div class="dialog-overlay" v-if="tpShowDialog" @click.self="tpShowDialog = false">
      <div class="dialog card" style="max-width: 520px;">
        <h2>{{ tpIsEditing ? '编辑' : '添加' }} {{ tpDialogLabel }} 预设</h2>
        <div class="dialog-scroll-area">
          <div class="form-group" v-if="!tpIsEditing">
            <label>预设名称</label>
            <input type="text" v-model="tpEditing.label" class="input-field" placeholder="例如: default, coding" />
          </div>
          <div class="form-group">
            <label>关联 Provider</label>
            <select v-model="tpEditing.provider" class="input-field">
              <option v-for="(_, pName) in tpCurrentCompatibleProviders" :key="pName" :value="pName">{{ pName }}</option>
            </select>
            <p v-if="Object.keys(tpCurrentCompatibleProviders).length === 0" class="tp-compat-warning">当前终端类型无可用的兼容 Provider。</p>
          </div>
          <div class="form-group">
            <label>模型 (留空使用 Provider 默认值)</label>
            <input type="text" v-model="tpEditing.model" class="input-field" placeholder="例如: claude-sonnet-4-6" />
          </div>
          <div class="form-grid-2">
            <div class="form-group"><label>Temperature</label><input type="number" v-model.number="tpEditing.parameters.temperature" class="input-field" step="0.1" min="0" max="1" placeholder="默认" /></div>
            <div class="form-group"><label>Top P</label><input type="number" v-model.number="tpEditing.parameters.top_p" class="input-field" step="0.1" min="0" max="1" placeholder="默认" /></div>
            <div class="form-group"><label>Max Tokens</label><input type="number" v-model.number="tpEditing.parameters.max_tokens" class="input-field" step="1" min="1" placeholder="默认" /></div>
            <div class="form-group"><label>Stream</label><select v-model="tpStreamValue" class="input-field"><option value="">默认</option><option value="true">启用</option><option value="false">禁用</option></select></div>
          </div>
          <div class="form-grid-2" style="margin-top: 12px;">
            <div class="form-group"><label>Thinking 模式</label><select v-model="tpThinkingType" class="input-field"><option value="">默认</option><option value="disabled">禁用</option><option value="enabled">启用</option></select></div>
            <div class="form-group" v-if="tpThinkingType === 'enabled'"><label>Budget Tokens</label><input type="number" v-model.number="tpThinkingBudget" class="input-field" step="1" min="1024" placeholder="16384" /></div>
          </div>
          <div class="tp-section-divider"></div>
          <div class="form-grid-2">
            <div class="form-group"><label>Context Window</label><input type="number" v-model.number="tpContextWindow" class="input-field" step="1" min="1" placeholder="默认" /></div>
            <div class="form-group"><label>Auto Compact Threshold</label><input type="number" v-model.number="tpCompactLimit" class="input-field" step="1" min="1" placeholder="默认" /></div>
          </div>
        </div>
        <div class="dialog-actions">
          <button class="btn secondary" @click="tpShowDialog = false">取消</button>
          <button class="btn primary" @click="tpHandleSave" :disabled="(!tpEditing.label && !tpEditing.name) || !tpEditing.provider">保存</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, reactive, computed, onMounted, watch, nextTick } from 'vue'
import { GetProviders, SaveProvider, DeleteProvider } from '../../wailsjs/go/config/ConfigService'
import { GetOpenCodePresets, SaveOpenCodePreset, DeleteOpenCodePreset } from '../../wailsjs/go/config/ConfigService'
import { ImportConfigFromFile, ExportConfigToFile, GetTerminalPresets, SaveTerminalPreset, DeleteTerminalPreset, GetOpenCodeConfig, SaveOpenCodeConfig, GetOpenCodeConfigPath } from '../../wailsjs/go/main/App'
import { HasAPIKey } from '../../wailsjs/go/secrets/SecretsService'
import { config } from '../../wailsjs/go/models'
import { useToast } from '../composables/useToast'
import ProviderDetail from './ProviderDetail.vue'

const { showSuccess, showError } = useToast()

// ===== Navigation =====
const activeSection = ref('providers')
const navTabs = [
  { id: 'providers', label: '服务提供商', icon: '☁' },
  { id: 'claude-code', label: 'Claude Code', icon: '◂' },
  { id: 'opencode', label: 'OpenCode', icon: '⊏' },
  { id: 'codex', label: 'Codex', icon: '◊' },
]

// ===== Provider List =====
const providers = ref<Record<string, config.Provider>>({})
const apiKeyStatus = ref<Record<string, boolean>>({})
const loading = ref(false)
const exporting = ref(false)
const filterType = ref<'all' | 'anthropic' | 'openai'>('all')
const selectedProviderName = ref('')

// Add dialog
const showAddDialog = ref(false)
const newProviderName = ref('')
const newProviderSupportsAnthropic = ref(true)
const newProviderSupportsOpenAI = ref(false)
const newProviderAnthropicBaseUrl = ref('')
const newProviderOpenAIBaseUrl = ref('')
const newProviderModel = ref('')

function hasAnthropicFormat(p: any): boolean {
  return !!(p?.anthropic?.enabled) || ((!p?.openai?.enabled) && (p?.type || 'anthropic') !== 'openai' && p?.auth_key !== 'OPENAI_API_KEY')
}
function hasOpenAIFormat(p: any): boolean {
  return !!(p?.openai?.enabled) || (p?.type || '').toLowerCase() === 'openai' || p?.auth_key === 'OPENAI_API_KEY'
}

function resetAddProviderForm() {
  showAddDialog.value = false
  newProviderName.value = ''
  newProviderSupportsAnthropic.value = true
  newProviderSupportsOpenAI.value = false
  newProviderAnthropicBaseUrl.value = ''
  newProviderOpenAIBaseUrl.value = ''
  newProviderModel.value = ''
}

function openAddProviderDialog() {
  resetAddProviderForm()
  showAddDialog.value = true
}

const filteredProviders = computed(() => {
  if (filterType.value === 'all') return providers.value
  const result: Record<string, config.Provider> = {}
  for (const [name, p] of Object.entries(providers.value)) {
    const isA = hasAnthropicFormat(p as any)
    const isO = hasOpenAIFormat(p as any)
    if (filterType.value === 'anthropic' && isA) result[name] = p
    if (filterType.value === 'openai' && isO) result[name] = p
  }
  return result
})

const loadProviders = async () => {
  loading.value = true
  try {
    const records = await GetProviders()
    const statusEntries = await Promise.all(
      Object.keys(records).map(async (name) => {
        const hasMain = await HasAPIKey(name)
        if (hasMain) return [name, true] as const
        const [hasLegacy] = await Promise.all([
          HasAPIKey(name + ':anthropic'),
        ])
        if (hasLegacy) return [name, true] as const
        const hasLegacy2 = await HasAPIKey(name + ':openai')
        return [name, hasLegacy2] as const
      })
    )
    providers.value = records
    apiKeyStatus.value = Object.fromEntries(statusEntries)
  } catch (err) {
    showError('加载提供商失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleAddProvider = async () => {
  if (!newProviderName.value) return
  if (!newProviderSupportsAnthropic.value && !newProviderSupportsOpenAI.value) {
    showError('至少启用一种 Provider 格式')
    return
  }
  loading.value = true
  try {
    const p = new config.Provider({
      default_model: newProviderModel.value,
      presets: {},
    } as any)
    if (newProviderSupportsAnthropic.value) {
      p.anthropic = new config.AnthropicFormat({
        enabled: true,
        base_url: newProviderAnthropicBaseUrl.value,
        auth_key: 'ANTHROPIC_API_KEY',
      })
    }
    if (newProviderSupportsOpenAI.value) {
      p.openai = new config.OpenAIFormat({
        enabled: true,
        base_url: newProviderOpenAIBaseUrl.value,
        auth_key: 'OPENAI_API_KEY',
      })
    }
    await SaveProvider(newProviderName.value, p)
    resetAddProviderForm()
    await loadProviders()
    showSuccess('添加提供商成功')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleDeleteProvider = async (name: string) => {
  if (!confirm(`确定要删除提供商 "${name}" 吗？`)) return
  loading.value = true
  try {
    await DeleteProvider(name)
    await loadProviders()
    showSuccess('删除成功')
  } catch (err) {
    showError('删除失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleExportConfig = async () => {
  exporting.value = true
  try {
    const savedPath = await ExportConfigToFile()
    if (savedPath) showSuccess('配置已导出到: ' + savedPath)
  } catch (err) {
    showError('导出失败: ' + err)
  } finally {
    exporting.value = false
  }
}

const handleImportConfig = async () => {
  loading.value = true
  try {
    const result = await ImportConfigFromFile()
    if (result) {
      await loadProviders()
      showSuccess(result)
    }
  } catch (err) {
    showError('导入失败: ' + err)
  } finally {
    loading.value = false
  }
}

// ===== Terminal Presets =====
interface TerminalPresetData {
  name: string
  label: string
  provider: string
  model: string
  parameters: {
    temperature?: number
    top_p?: number
    max_tokens?: number
    stream?: boolean
    thinking?: { type: string; budgetTokens?: number }
    context_window?: { model_context_window?: number; model_auto_compact_token_limit?: number }
  }
}

const tpPresets = ref<Record<string, TerminalPresetData[]>>({
  claude_code: [],
  opencode: [],
  codex: [],
})

// Claude Code presets grouped by provider
const claudeCodePresetsByProvider = computed(() => {
  const groups: Record<string, TerminalPresetData[]> = {}
  for (const p of tpPresets.value.claude_code) {
    const key = p.provider || '(未关联)'
    if (!groups[key]) groups[key] = []
    groups[key].push(p)
  }
  return groups
})

const claudeCodeProviderNames = computed(() => Object.keys(claudeCodePresetsByProvider.value).sort())

// Codex presets grouped by provider
const codexPresetsByProvider = computed(() => {
  const groups: Record<string, TerminalPresetData[]> = {}
  for (const p of tpPresets.value.codex) {
    const key = p.provider || '(未关联)'
    if (!groups[key]) groups[key] = []
    groups[key].push(p)
  }
  return groups
})

const codexProviderNames = computed(() => Object.keys(codexPresetsByProvider.value).sort())

// Provider filter tabs for Claude Code / Codex
const claudeCodeFilterProvider = ref('')
const codexFilterProvider = ref('')

const filteredClaudeCodePresets = computed(() => {
  if (!claudeCodeFilterProvider.value) return tpPresets.value.claude_code
  return tpPresets.value.claude_code.filter(p => p.provider === claudeCodeFilterProvider.value)
})

const filteredCodexPresets = computed(() => {
  if (!codexFilterProvider.value) return tpPresets.value.codex
  return tpPresets.value.codex.filter(p => p.provider === codexFilterProvider.value)
})

// OpenCode preset search
const ocPresetSearchQuery = ref('')
const filteredOcPresetList = computed(() => {
  const q = ocPresetSearchQuery.value.trim().toLowerCase()
  if (!q) return ocPresetList.value
  return ocPresetList.value.filter(p =>
    p.name.toLowerCase().includes(q) || (p.description && p.description.toLowerCase().includes(q))
  )
})

// Dialog state (shared across 3 tabs)
const tpShowDialog = ref(false)
const tpIsEditing = ref(false)
const tpDialogTarget = ref('') // 'claude_code' | 'opencode' | 'codex'
const tpEditingOriginalName = ref('')
const tpEditing = ref<TerminalPresetData>({
  name: '', label: '', provider: '', model: '', parameters: {},
})
const tpThinkingType = ref('')
const tpThinkingBudget = ref<number | undefined>(undefined)
const tpStreamValue = ref('')
const tpContextWindow = ref<number | undefined>(undefined)
const tpCompactLimit = ref<number | undefined>(undefined)

const tpDialogLabel = computed(() => {
  if (tpDialogTarget.value === 'claude_code') return 'Claude Code'
  if (tpDialogTarget.value === 'opencode') return 'OpenCode'
  return 'Codex'
})

const tpCurrentCompatibleProviders = computed(() => {
  const tt = tpDialogTarget.value
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (tt === 'claude_code') {
      if (hasAnthropicFormat(provider as any)) result[name] = provider
    } else {
      if (hasOpenAIFormat(provider as any)) result[name] = provider
    }
  }
  return result
})

async function tpLoadAll() {
  const types = ['claude_code', 'codex']
  for (const tt of types) {
    try {
      const map = await GetTerminalPresets(tt)
      const list: TerminalPresetData[] = []
      for (const [key, p] of Object.entries(map || {})) {
        const raw = p as any
        list.push({
          name: key,
          label: raw.name || key,
          provider: raw.provider || '',
          model: raw.model || '',
          parameters: raw.parameters || {},
        })
      }
      tpPresets.value[tt] = list
    } catch {
      tpPresets.value[tt] = []
    }
  }
}

function tpOpenAdd(terminalType: string) {
  tpDialogTarget.value = terminalType
  tpIsEditing.value = false
  tpEditingOriginalName.value = ''
  tpEditing.value = { name: '', label: '', provider: '', model: '', parameters: {} }
  tpThinkingType.value = ''
  tpThinkingBudget.value = undefined
  tpStreamValue.value = ''
  tpContextWindow.value = undefined
  tpCompactLimit.value = undefined
  const compatKeys = Object.keys(tpCurrentCompatibleProviders.value)
  if (compatKeys.length > 0) tpEditing.value.provider = compatKeys[0]
  tpShowDialog.value = true
}

function tpOpenEdit(terminalType: string, preset: TerminalPresetData) {
  tpDialogTarget.value = terminalType
  tpIsEditing.value = true
  tpEditingOriginalName.value = preset.name
  tpEditing.value = JSON.parse(JSON.stringify(preset))
  if (preset.parameters?.thinking?.type) {
    tpThinkingType.value = preset.parameters.thinking.type
    tpThinkingBudget.value = preset.parameters.thinking.budgetTokens
  } else {
    tpThinkingType.value = ''
    tpThinkingBudget.value = undefined
  }
  tpStreamValue.value = preset.parameters?.stream !== undefined ? (preset.parameters.stream ? 'true' : 'false') : ''
  if (preset.parameters?.context_window) {
    tpContextWindow.value = preset.parameters.context_window.model_context_window
    tpCompactLimit.value = preset.parameters.context_window.model_auto_compact_token_limit
  } else {
    tpContextWindow.value = undefined
    tpCompactLimit.value = undefined
  }
  tpShowDialog.value = true
}

async function tpHandleSave() {
  let stableKey = tpEditingOriginalName.value
  if (!stableKey) {
    const userLabel = (tpEditing.value.label || tpEditing.value.name || '').trim()
    if (!userLabel || !tpEditing.value.provider) return
    stableKey = tpEditing.value.provider + '/' + userLabel
  }
  const friendlyName = (tpEditing.value.label || tpEditing.value.name || '').trim()

  try {
    const src = tpEditing.value.parameters || {}
    const managed: Record<string, any> = {}
    if (typeof src.temperature === 'number' && isFinite(src.temperature)) managed.temperature = src.temperature
    if (typeof src.top_p === 'number' && isFinite(src.top_p)) managed.top_p = src.top_p
    if (typeof src.max_tokens === 'number' && isFinite(src.max_tokens) && src.max_tokens > 0) managed.max_tokens = Math.floor(src.max_tokens)
    if (tpStreamValue.value === 'true') managed.stream = true
    else if (tpStreamValue.value === 'false') managed.stream = false
    if (tpThinkingType.value === 'enabled' || tpThinkingType.value === 'disabled') {
      const thinking: Record<string, any> = { type: tpThinkingType.value }
      if (tpThinkingType.value === 'enabled' && typeof tpThinkingBudget.value === 'number' && tpThinkingBudget.value > 0) thinking.budgetTokens = Math.floor(tpThinkingBudget.value)
      managed.thinking = thinking
    }
    const hasCtxWindow = typeof tpContextWindow.value === 'number' && tpContextWindow.value > 0
    const hasCompact = typeof tpCompactLimit.value === 'number' && tpCompactLimit.value > 0
    if (hasCtxWindow || hasCompact) {
      const ctx: Record<string, any> = {}
      if (hasCtxWindow) ctx.model_context_window = Math.floor(tpContextWindow.value!)
      if (hasCompact) ctx.model_auto_compact_token_limit = Math.floor(tpCompactLimit.value!)
      managed.context_window = ctx
    }
    const MANAGED_KEYS = new Set(['temperature', 'top_p', 'max_tokens', 'stream', 'thinking', 'context_window'])
    const cleanParams: Record<string, any> = {}
    for (const [k, v] of Object.entries(src)) { if (!MANAGED_KEYS.has(k)) cleanParams[k] = v }
    for (const [k, v] of Object.entries(managed)) cleanParams[k] = v

    const payload: Record<string, any> = {
      name: friendlyName,
      provider: tpEditing.value.provider,
      model: tpEditing.value.model,
      parameters: cleanParams,
    }
    await SaveTerminalPreset(tpDialogTarget.value, stableKey, payload as any)
    tpShowDialog.value = false
    await tpLoadAll()
    showSuccess('终端预设已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  }
}

async function tpHandleDelete(terminalType: string, name: string) {
  if (!confirm(`确定要删除预设 "${name}" 吗？`)) return
  try {
    await DeleteTerminalPreset(terminalType, name)
    await tpLoadAll()
    showSuccess('已删除')
  } catch (err) {
    showError('删除失败: ' + err)
  }
}

// ===== OpenCode Presets (new model) =====
interface OcPresetListEntry {
  key: string
  name: string
  description: string
  bindingCount: number
}

interface OcPresetBinding {
  providerId: string
  localProvider: string
  format: string
  injectApiKey: boolean
  injectBaseURL: boolean
  injectOrganization: boolean
}

const ocPresetList = ref<OcPresetListEntry[]>([])
const ocPresetRawMap = ref<Record<string, any>>({})
const ocPresetShowDialog = ref(false)
const ocPresetIsEditing = ref(false)
const ocPresetConfigJson = ref('{}')
const ocPresetConfigError = ref('')
const ocPresetEditing = ref<{
  key: string
  name: string
  description: string
  bindings: OcPresetBinding[]
}>({
  key: '',
  name: '',
  description: '',
  bindings: [],
})

async function ocPresetLoadAll() {
  try {
    const map = await GetOpenCodePresets()
    ocPresetRawMap.value = map || {}
    const list: OcPresetListEntry[] = []
    for (const [key, preset] of Object.entries(map || {})) {
      const p = preset as any
      list.push({
        key,
        name: p.name || key,
        description: p.description || '',
        bindingCount: p.bindings ? Object.keys(p.bindings).length : 0,
      })
    }
    ocPresetList.value = list
  } catch {
    ocPresetList.value = []
    ocPresetRawMap.value = {}
  }
}

function ocPresetOpenAdd() {
  ocPresetIsEditing.value = false
  ocPresetEditing.value = { key: '', name: '', description: '', bindings: [] }
  ocPresetConfigJson.value = '{\n  "model": ""\n}\n'
  ocPresetConfigError.value = ''
  ocPresetShowDialog.value = true
}

function ocPresetOpenEdit(entry: OcPresetListEntry) {
  ocPresetIsEditing.value = true
  const raw = ocPresetRawMap.value[entry.key] as any
  const bindings: OcPresetBinding[] = []
  if (raw && raw.bindings && typeof raw.bindings === 'object') {
    for (const [pid, b] of Object.entries(raw.bindings as Record<string, any>)) {
      const inject: string[] = (b && b.inject) || []
      bindings.push({
        providerId: pid,
        localProvider: (b && b.local_provider) || '',
        format: (b && b.format) || '',
        injectApiKey: inject.includes('apiKey'),
        injectBaseURL: inject.includes('baseURL'),
        injectOrganization: inject.includes('organization'),
      })
    }
  }
  ocPresetEditing.value = {
    key: entry.key,
    name: raw?.name || entry.name,
    description: raw?.description || '',
    bindings,
  }
  ocPresetConfigJson.value = raw?.config ? JSON.stringify(raw.config, null, 2) : '{}'
  ocPresetConfigError.value = ''
  ocPresetShowDialog.value = true
}

function ocPresetAddBinding() {
  ocPresetEditing.value.bindings.push({
    providerId: '',
    localProvider: '',
    format: '',
    injectApiKey: true,
    injectBaseURL: false,
    injectOrganization: false,
  })
}

// Validate config JSON
watch(ocPresetConfigJson, (val) => {
  try {
    JSON.parse(val)
    ocPresetConfigError.value = ''
  } catch (e: any) {
    ocPresetConfigError.value = 'JSON 格式错误: ' + (e.message || '').substring(0, 80)
  }
})

async function ocPresetHandleSave() {
  const { key, name, description, bindings } = ocPresetEditing.value
  if (!key || !name) return

  // Parse config
  let configObj: any
  try {
    configObj = JSON.parse(ocPresetConfigJson.value)
  } catch {
    showError('JSON 配置格式错误')
    return
  }

  // Build bindings map
  const bindingsMap: Record<string, any> = {}
  for (const b of bindings) {
    if (!b.providerId) continue
    const inject: string[] = []
    if (b.injectApiKey) inject.push('apiKey')
    if (b.injectBaseURL) inject.push('baseURL')
    if (b.injectOrganization) inject.push('organization')
    const entry: Record<string, any> = { local_provider: b.localProvider }
    if (b.format) entry.format = b.format
    if (inject.length > 0) entry.inject = inject
    bindingsMap[b.providerId] = entry
  }

  try {
    await SaveOpenCodePreset(key, {
      name,
      description,
      config: configObj,
      bindings: Object.keys(bindingsMap).length > 0 ? bindingsMap : undefined,
    } as any)
    ocPresetShowDialog.value = false
    await ocPresetLoadAll()
    showSuccess('OpenCode 预设已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  }
}

async function ocPresetHandleDelete(key: string) {
  if (!confirm(`确定要删除预设 "${key}" 吗？`)) return
  try {
    await DeleteOpenCodePreset(key)
    await ocPresetLoadAll()
    showSuccess('已删除')
  } catch (err) {
    showError('删除失败: ' + err)
  }
}

// ===== OpenCode Global Config =====
const ocConfigPath = ref('')
const ocEditorContent = ref('')
const ocDiskContent = ref('')
const ocSaving = ref(false)
const ocReloading = ref(false)
const ocReverting = ref(false)
const ocEditMode = ref<'visual' | 'json'>('visual')
const ocSwitchBlocked = ref(false)

interface OcKvPair { key: string; value: string }
interface OcProviderModelEntry { key: string; name: string; variants: string[]; optionsRaw: string; preserved: Record<string, any> }
interface OcProviderEntry { name: string; apiKey: string; baseURL: string; models: OcProviderModelEntry[]; preserved: Record<string, any> }
interface OcAgentEntry { name: string; description: string; mode: 'primary' | 'subagent'; model: string; variant: string; color: string; prompt: string; preserved: Record<string, any> }
interface OcMcpEntry { name: string; type: 'remote' | 'local'; url: string; commandArgs: string[]; headers: OcKvPair[]; environment: OcKvPair[]; oauth: boolean; preserved: Record<string, any> }
interface OcPermEntry { key: string; value: string }
interface OcKvEntry { key: string; valueRaw: string }

const ocGui = reactive({
  modelValue: '',
  providers: [] as OcProviderEntry[],
  agents: [] as OcAgentEntry[],
  mcpServers: [] as OcMcpEntry[],
  permissions: [] as OcPermEntry[],
  instructions: [] as string[],
  plugins: [] as string[],
  experimentalKvs: [] as OcKvEntry[],
  unknownFieldsRaw: '',
})

const ocSections = reactive<Record<string, boolean>>({
  model: true, provider: false, agent: false, mcp: false,
  permission: false, instructions: false, plugin: false, experimental: false,
})
const OC_KNOWN_KEYS = new Set(['model', 'provider', 'agent', 'mcp', 'permission', 'instructions', 'plugin', 'experimental', '$schema'])
const ocToggleSection = (s: string) => { ocSections[s] = !ocSections[s] }

const ocSubJsonErrors = reactive<Record<string, string>>({})
function clearOcSubJsonErrors() { for (const k of Object.keys(ocSubJsonErrors)) delete ocSubJsonErrors[k] }

function ocClone<T>(value: T): T {
  if (value === undefined) return value
  return JSON.parse(JSON.stringify(value))
}

function ocToKvPairs(value: any): OcKvPair[] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return []
  return Object.entries(value as Record<string, any>).map(([key, rawValue]) => ({ key, value: rawValue == null ? '' : String(rawValue) }))
}

function ocKvPairsToRecord(pairs: OcKvPair[]): Record<string, string> | undefined {
  const record: Record<string, string> = {}
  for (const pair of pairs) {
    const key = pair.key.trim()
    if (!key) continue
    record[key] = pair.value
  }
  return Object.keys(record).length > 0 ? record : undefined
}

function ocParseVariantKeys(value: any): string[] {
  if (Array.isArray(value)) return value.map(v => String(v).trim()).filter(Boolean)
  if (value && typeof value === 'object') return Object.keys(value).filter(Boolean)
  return []
}

function ocParseVariantInput(input: string): string[] {
  return input.split(',').map(v => v.trim()).filter(Boolean)
}

function isValidHexColor(value: string): boolean {
  return /^#(?:[0-9a-fA-F]{6}|[0-9a-fA-F]{3})$/.test(value.trim())
}

function normalizeHexColor(value: string): string {
  const trimmed = value.trim()
  if (!isValidHexColor(trimmed)) return '#808080'
  if (trimmed.length === 4) {
    const [, r, g, b] = trimmed
    return `#${r}${r}${g}${g}${b}${b}`.toLowerCase()
  }
  return trimmed.toLowerCase()
}

function ocAgentPickerColor(color: string): string {
  return isValidHexColor(color) ? normalizeHexColor(color) : '#808080'
}

function ocSetAgentColorFromPicker(agent: OcAgentEntry, value: string) {
  agent.color = normalizeHexColor(value)
  ocGuiToRaw()
}

function ocSetAgentColorFromPickerEvent(agent: OcAgentEntry, event: Event) {
  ocSetAgentColorFromPicker(agent, (event.target as HTMLInputElement)?.value || '#808080')
}

const ocRawToGui = () => {
  clearOcSubJsonErrors()
  const raw = ocEditorContent.value.trim()
  if (!raw) { Object.assign(ocGui, { modelValue:'', providers:[], agents:[], mcpServers:[], permissions:[], instructions:[], plugins:[], experimentalKvs:[], unknownFieldsRaw:'' }); return }
  let obj: any
  try { obj = JSON.parse(raw) } catch { return }
  if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) return
  const rootUnknown: Record<string, any> = {}
  for (const [key, value] of Object.entries(obj)) {
    if (!OC_KNOWN_KEYS.has(key)) rootUnknown[key] = value
  }
  ocGui.modelValue = typeof obj.model === 'string' ? obj.model : ''
  const provs: OcProviderEntry[] = []
  if (obj.provider && typeof obj.provider === 'object') {
    for (const [n, e] of Object.entries(obj.provider as Record<string, any>)) {
      if (!e || typeof e !== 'object') continue
      const opts = e.options && typeof e.options === 'object' ? e.options : {}
      const models: OcProviderModelEntry[] = []
      if (e.models && typeof e.models === 'object' && !Array.isArray(e.models)) {
        for (const [modelKey, modelEntry] of Object.entries(e.models as Record<string, any>)) {
          if (!modelEntry || typeof modelEntry !== 'object' || Array.isArray(modelEntry)) continue
          models.push({
            key: modelKey,
            name: typeof modelEntry.name === 'string' ? modelEntry.name : '',
            variants: ocParseVariantKeys(modelEntry.variants),
            optionsRaw: modelEntry.options && typeof modelEntry.options === 'object' ? JSON.stringify(modelEntry.options, null, 2) : '',
            preserved: ocClone(modelEntry),
          })
        }
      }
      provs.push({ name: n, apiKey: opts.apiKey || '', baseURL: opts.baseURL || '', models, preserved: ocClone(e) })
    }
  }
  ocGui.providers = provs
  const agents: OcAgentEntry[] = []
  if (obj.agent && typeof obj.agent === 'object') {
    for (const [n, e] of Object.entries(obj.agent as Record<string, any>)) {
      if (!e || typeof e !== 'object') continue
      agents.push({ name: n, description: e.description || '', mode: e.mode === 'primary' ? 'primary' : 'subagent', model: e.model || '', variant: typeof e.variant === 'string' ? e.variant : '', color: e.color || '', prompt: e.prompt || '', preserved: ocClone(e) })
    }
  }
  ocGui.agents = agents
  const mcps: OcMcpEntry[] = []
  if (obj.mcp && typeof obj.mcp === 'object') {
    for (const [n, e] of Object.entries(obj.mcp as Record<string, any>)) {
      if (!e || typeof e !== 'object') continue
      let ca: string[] = []
      if (Array.isArray(e.command)) ca = e.command.map((s: any) => String(s))
      mcps.push({
        name: n,
        type: e.type === 'local' ? 'local' : 'remote',
        url: e.url || '',
        commandArgs: ca,
        headers: ocToKvPairs(e.headers),
        environment: ocToKvPairs(e.environment),
        oauth: e.oauth === true,
        preserved: ocClone(e),
      })
    }
  }
  ocGui.mcpServers = mcps
  const perms: OcPermEntry[] = []
  if (obj.permission && typeof obj.permission === 'object') {
    for (const [k, v] of Object.entries(obj.permission as Record<string, any>)) perms.push({ key: k, value: String(v) })
  }
  ocGui.permissions = perms
  ocGui.instructions = Array.isArray(obj.instructions) ? obj.instructions.filter((s: any) => typeof s === 'string') : []
  ocGui.plugins = Array.isArray(obj.plugin) ? obj.plugin.map((p: any) => typeof p === 'string' ? p : JSON.stringify(p)) : []
  const exps: OcKvEntry[] = []
  if (obj.experimental && typeof obj.experimental === 'object') {
    for (const [k, v] of Object.entries(obj.experimental as Record<string, any>)) exps.push({ key: k, valueRaw: JSON.stringify(v) })
  }
  ocGui.experimentalKvs = exps
  ocGui.unknownFieldsRaw = Object.keys(rootUnknown).length > 0 ? JSON.stringify(rootUnknown, null, 2) : ''
}

const ocGuiToRaw = () => {
  clearOcSubJsonErrors()
  const result: Record<string, any> = {}
  if (ocGui.unknownFieldsRaw.trim()) {
    try {
      const unknown = JSON.parse(ocGui.unknownFieldsRaw)
      if (!unknown || typeof unknown !== 'object' || Array.isArray(unknown)) ocSubJsonErrors.unknownFields = '顶层保留字段必须为 JSON 对象'
      else Object.assign(result, unknown)
    } catch {
      ocSubJsonErrors.unknownFields = '顶层保留字段 JSON 非法'
    }
  }
  if (ocGui.modelValue.trim()) result.model = ocGui.modelValue.trim()
  if (ocGui.providers.length > 0) {
    const provider: Record<string, any> = {}
    for (const p of ocGui.providers) {
      const providerName = p.name.trim()
      if (!providerName) continue
      const entry: Record<string, any> = p.preserved && typeof p.preserved === 'object' ? ocClone(p.preserved) : {}
      const options: Record<string, any> = entry.options && typeof entry.options === 'object' && !Array.isArray(entry.options) ? ocClone(entry.options) : {}
      delete options.apiKey
      delete options.baseURL
      if (p.apiKey.trim()) options.apiKey = p.apiKey.trim()
      if (p.baseURL.trim()) options.baseURL = p.baseURL.trim()
      if (Object.keys(options).length > 0) entry.options = options
      else delete entry.options
      if (p.models.length > 0) {
        const models: Record<string, any> = {}
        for (const model of p.models) {
          const modelKey = model.key.trim()
          if (!modelKey) continue
          const modelEntry: Record<string, any> = model.preserved && typeof model.preserved === 'object' ? ocClone(model.preserved) : {}
          delete modelEntry.name
          delete modelEntry.variants
          delete modelEntry.options
          if (model.name.trim()) modelEntry.name = model.name.trim()
          const variantKeys = model.variants.map(v => v.trim()).filter(Boolean)
          if (variantKeys.length > 0) {
            modelEntry.variants = Object.fromEntries(variantKeys.map(v => [v, {}]))
          }
          if (model.optionsRaw.trim()) {
            try {
              const parsedOptions = JSON.parse(model.optionsRaw)
              modelEntry.options = parsedOptions
            } catch {
              ocSubJsonErrors['provider.' + providerName + '.models.' + modelKey + '.options'] = '无效 JSON'
            }
          }
          models[modelKey] = modelEntry
        }
        if (Object.keys(models).length > 0) entry.models = models
        else delete entry.models
      } else {
        delete entry.models
      }
      provider[providerName] = entry
    }
    if (Object.keys(provider).length > 0) result.provider = provider
  }
  if (ocGui.agents.length > 0) {
    const agent: Record<string, any> = {}
    for (const a of ocGui.agents) {
      const agentName = a.name.trim()
      if (!agentName) continue
      const entry: Record<string, any> = a.preserved && typeof a.preserved === 'object' ? ocClone(a.preserved) : {}
      delete entry.description
      delete entry.mode
      delete entry.model
      delete entry.variant
      delete entry.color
      delete entry.prompt
      if (a.description.trim()) entry.description = a.description.trim()
      if (a.mode) entry.mode = a.mode
      if (a.model.trim()) entry.model = a.model.trim()
      if (a.variant.trim()) entry.variant = a.variant.trim()
      if (a.color.trim()) entry.color = a.color.trim()
      if (a.prompt.trim()) entry.prompt = a.prompt.trim()
      agent[agentName] = entry
    }
    if (Object.keys(agent).length > 0) result.agent = agent
  }
  if (ocGui.mcpServers.length > 0) {
    const mcp: Record<string, any> = {}
    for (const m of ocGui.mcpServers) {
      const mcpName = m.name.trim()
      if (!mcpName) continue
      const entry: Record<string, any> = m.preserved && typeof m.preserved === 'object' ? ocClone(m.preserved) : {}
      delete entry.type
      delete entry.url
      delete entry.command
      delete entry.headers
      delete entry.environment
      delete entry.oauth
      entry.type = m.type
      if (m.type === 'remote' && m.url.trim()) entry.url = m.url.trim()
      if (m.type === 'local') {
        const filteredArgs = m.commandArgs.filter(a => a.trim())
        if (filteredArgs.length > 0) entry.command = filteredArgs
      }
      const headers = ocKvPairsToRecord(m.headers)
      if (headers) entry.headers = headers
      const environment = ocKvPairsToRecord(m.environment)
      if (environment) entry.environment = environment
      if (m.oauth) entry.oauth = true
      mcp[mcpName] = entry
    }
    if (Object.keys(mcp).length > 0) result.mcp = mcp
  }
  if (ocGui.permissions.length > 0) { const perm: Record<string, string> = {}; for (const p of ocGui.permissions) { if (p.key.trim()) perm[p.key.trim()] = p.value } if (Object.keys(perm).length > 0) result.permission = perm }
  const instrs = ocGui.instructions.filter(s => s.trim()); if (instrs.length > 0) result.instructions = instrs
  const plugs = ocGui.plugins.filter(s => s.trim()); if (plugs.length > 0) result.plugin = plugs
  if (ocGui.experimentalKvs.length > 0) { const exp: Record<string, any> = {}; for (const kv of ocGui.experimentalKvs) { const k = kv.key.trim(); if (!k || !kv.valueRaw.trim()) continue; try { exp[k] = JSON.parse(kv.valueRaw) } catch { ocSubJsonErrors['experimental.' + k] = '无效 JSON' } } if (Object.keys(exp).length > 0) result.experimental = exp }
  ocEditorContent.value = Object.keys(result).length > 0 ? JSON.stringify(result, null, 2) + '\n' : '{\n}\n'
}

const ocValidationError = computed<string | null>(() => {
  const text = ocEditorContent.value.trim()
  if (!text) return null
  try { const p = JSON.parse(text); if (p === null) return '不能为 null'; if (Array.isArray(p)) return '必须为对象'; if (typeof p !== 'object') return '必须为对象'; return '' } catch (e: any) { return (e.message || '').substring(0, 140) }
})
const ocIsParseableJson = computed(() => { try { JSON.parse(ocEditorContent.value.trim()); return true } catch { return false } })
const ocHasSubJsonErrors = computed(() => Object.keys(ocSubJsonErrors).length > 0)
const ocCanSave = computed(() => { if (ocValidationError.value !== '') return false; if (ocEditMode.value === 'visual' && ocHasSubJsonErrors.value) return false; return true })
const ocValidationClass = computed(() => { if (ocEditMode.value === 'visual' && ocHasSubJsonErrors.value) return 'invalid'; if (ocValidationError.value === null) return 'neutral'; if (ocValidationError.value === '') return 'valid'; return 'invalid' })
const ocValidationText = computed(() => { if (ocEditMode.value === 'visual' && ocHasSubJsonErrors.value) return '字段错误'; if (ocValidationError.value === null) return '空'; if (ocValidationError.value === '') return 'JSON 合法'; return 'JSON 非法' })
const ocHasUnsavedChanges = computed(() => ocEditorContent.value !== ocDiskContent.value)

const ocSwitchToVisual = () => {
  if (ocValidationError.value !== '' && ocValidationError.value !== null) { ocSwitchBlocked.value = true; return }
  ocSwitchBlocked.value = false; ocRawToGui(); ocEditMode.value = 'visual'
}
const ocSwitchToJson = () => {
  if (ocEditMode.value === 'visual') ocGuiToRaw()
  clearOcSubJsonErrors(); ocSwitchBlocked.value = false; ocEditMode.value = 'json'
}

// Section add/remove helpers
const ocAddProvider = () => { ocGui.providers.push({ name: '', apiKey: '', baseURL: '', models: [], preserved: {} }); ocSections.provider = true }
const ocRemoveProvider = (i: number) => { ocGui.providers.splice(i, 1); ocGuiToRaw() }
const ocAddProviderModel = (provider: OcProviderEntry) => { provider.models.push({ key: '', name: '', variants: [], optionsRaw: '', preserved: {} }); ocSections.provider = true; ocGuiToRaw() }
const ocUpdateProviderModelVariants = (model: OcProviderModelEntry, value: string) => { model.variants = ocParseVariantInput(value); ocGuiToRaw() }
const ocUpdateProviderModelVariantsFromEvent = (model: OcProviderModelEntry, event: Event) => { ocUpdateProviderModelVariants(model, (event.target as HTMLInputElement)?.value || '') }
const ocAddAgent = () => { ocGui.agents.push({ name: '', description: '', mode: 'subagent', model: '', variant: '', color: '', prompt: '', preserved: {} }); ocSections.agent = true }
const ocRemoveAgent = (i: number) => { ocGui.agents.splice(i, 1); ocGuiToRaw() }
const ocAddMcp = () => { ocGui.mcpServers.push({ name: '', type: 'remote', url: '', commandArgs: [], headers: [], environment: [], oauth: false, preserved: {} }); ocSections.mcp = true }
const ocRemoveMcp = (i: number) => { ocGui.mcpServers.splice(i, 1); ocGuiToRaw() }
const ocAddPermission = () => { ocGui.permissions.push({ key: '', value: 'allow' }); ocSections.permission = true }
const ocRemovePermission = (i: number) => { ocGui.permissions.splice(i, 1); ocGuiToRaw() }
const ocAddInstruction = () => { ocGui.instructions.push(''); ocSections.instructions = true }
const ocRemoveInstruction = (i: number) => { ocGui.instructions.splice(i, 1); ocGuiToRaw() }
const ocAddPlugin = () => { ocGui.plugins.push(''); ocSections.plugin = true }
const ocRemovePlugin = (i: number) => { ocGui.plugins.splice(i, 1); ocGuiToRaw() }
const ocAddExperimentalKv = () => { ocGui.experimentalKvs.push({ key: '', valueRaw: '' }); ocSections.experimental = true }
const ocRemoveExperimentalKv = (i: number) => { ocGui.experimentalKvs.splice(i, 1); ocGuiToRaw() }

async function ocLoad() {
  try {
    const [content, path] = await Promise.all([GetOpenCodeConfig(), GetOpenCodeConfigPath()])
    ocEditorContent.value = content
    ocDiskContent.value = content
    ocConfigPath.value = path
    ocRawToGui()
  } catch (err) { showError('加载 OpenCode 配置失败: ' + err) }
}
async function ocReload() {
  ocReloading.value = true
  try { const c = await GetOpenCodeConfig(); ocEditorContent.value = c; ocDiskContent.value = c; ocRawToGui(); showSuccess('已重新加载') }
  catch (err) { showError('重新加载失败: ' + err) } finally { ocReloading.value = false }
}
async function ocSave() {
  if (!ocCanSave.value) return
  if (ocEditMode.value === 'visual') ocGuiToRaw()
  ocSaving.value = true
  try { await SaveOpenCodeConfig(ocEditorContent.value); const c = await GetOpenCodeConfig(); ocEditorContent.value = c; ocDiskContent.value = c; ocRawToGui(); showSuccess('OpenCode 配置已保存') }
  catch (err) { showError('保存失败: ' + err) } finally { ocSaving.value = false }
}
function ocFormat() { if (!ocIsParseableJson.value) return; try { ocEditorContent.value = JSON.stringify(JSON.parse(ocEditorContent.value), null, 2) + '\n' } catch {} }
async function ocRevert() {
  ocReverting.value = true
  try { const c = await GetOpenCodeConfig(); ocEditorContent.value = c; ocDiskContent.value = c; ocRawToGui(); showSuccess('已恢复') }
  catch (err) { showError('恢复失败: ' + err) } finally { ocReverting.value = false }
}
async function copyConfigPath() { try { await navigator.clipboard.writeText(ocConfigPath.value); showSuccess('路径已复制') } catch { showError('复制失败') } }

// ===== Lifecycle =====
onMounted(async () => {
  await loadProviders()
  await tpLoadAll()
  await ocPresetLoadAll()
})

watch(activeSection, (newSection) => {
  if (newSection === 'opencode' && !ocConfigPath.value) ocLoad()
  // Reset detail view when leaving providers tab
  if (newSection !== 'providers') selectedProviderName.value = ''
})
</script>

<style scoped>
/* Layout */
.pc-layout {
  --bg: #0f1219;
  --surface: #1a1f2e;
  --elevated: #232a3b;
  --border: #2a2f3e;
  --border-hover: #3a4f5e;
  --text-primary: #e0e6ed;
  --text-secondary: #8899aa;
  --text-muted: #5a6a7a;
  --accent: #4fc3f7;
  --accent-hover: #7bd4f9;
  --success: #66bb6a;
  --error: #ef5350;

  display: flex;
  height: 100%;
  gap: 40px;
  color: var(--text-primary);
}

/* Sidebar */
.pc-sidebar {
  width: 200px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
}
.pc-page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 24px;
}
.pc-nav { display: flex; flex-direction: column; gap: 6px; }
.pc-nav-tab {
  display: flex; align-items: center; gap: 12px;
  padding: 12px 16px; background: transparent; border: none;
  border-left: 3px solid transparent; border-radius: 0 6px 6px 0;
  color: var(--text-secondary); cursor: pointer; font-size: 14px;
  font-family: inherit; transition: background 0.2s, border-color 0.2s, color 0.2s;
  text-align: left;
}
.pc-nav-tab:hover { background: var(--surface); color: var(--text-primary); }
.pc-nav-tab.active { border-left-color: var(--accent); background: rgba(79,195,247,0.08); color: var(--accent); font-weight: 500; }
.pc-nav-icon { font-size: 16px; width: 20px; text-align: center; }
.pc-nav-label { }

/* Content */
.pc-content {
  flex: 1;
  overflow-y: auto;
  padding-right: 16px;
}
.pc-section { padding-bottom: 40px; }
.pc-section-header {
  display: flex; justify-content: space-between; align-items: flex-start;
  margin-bottom: 32px;
}
.pc-section-header h2 {
  font-size: 20px; font-weight: 600; color: var(--text-primary);
  margin: 0 0 8px 0;
}
.pc-section-header p { color: var(--text-secondary); font-size: 14px; margin: 0; }
.pc-header-actions { display: flex; gap: 8px; }
.pc-section-divider {
  height: 2px;
  background: linear-gradient(90deg, var(--accent), transparent);
  margin: 48px 0 32px;
  border-radius: 1px;
}

/* Provider List */
.provider-filter-tabs {
  display: flex; gap: 4px; background: #0f1219; border-radius: 8px; padding: 4px; width: fit-content;
  margin-bottom: 20px;
}
.filter-tab {
  padding: 8px 16px; background: transparent; border: none; border-radius: 6px;
  color: #8899aa; font-size: 13px; font-weight: 600; cursor: pointer; font-family: inherit;
  transition: all 0.15s ease;
}
.filter-tab:hover { color: #ccd6e0; background: rgba(255,255,255,0.05); }
.filter-tab.active { color: #4fc3f7; background: #1a1f2e; }

.provider-grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 20px;
}
.card { background: #1a1f2e; border: 1px solid #2a2f3e; border-radius: 8px; padding: 20px; }
.provider-card { cursor: pointer; transition: all 0.2s ease; }
.provider-card:hover { border-color: #4fc3f7; transform: translateY(-2px); box-shadow: 0 4px 12px rgba(0,0,0,0.2); }
.card-header { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 16px; }
.provider-title-group { display: flex; flex-direction: column; gap: 6px; }
.provider-name { margin: 0; font-size: 18px; font-weight: 700; color: #4fc3f7; }
.key-status-indicator { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: #8899aa; }
.status-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.status-dot.configured { background: #66bb6a; box-shadow: 0 0 0 3px rgba(102,187,106,0.12); }
.status-dot.unconfigured { background: #ffa726; box-shadow: 0 0 0 3px rgba(255,167,38,0.12); }
.info-row { display: flex; margin-bottom: 8px; font-size: 14px; }
.info-row .label { color: #8899aa; min-width: 80px; }
.info-row .value { color: #e0e6ed; flex: 1; }
.truncate { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.badge-row { margin-top: 16px; display: flex; gap: 6px; }
.badge { background: rgba(79,195,247,0.1); color: #4fc3f7; padding: 4px 10px; border-radius: 12px; font-size: 12px; font-weight: 600; }
.format-anthropic { background: rgba(230,126,34,0.1); color: #e67e22; }
.format-openai { background: rgba(16,163,127,0.1); color: #10a37f; }
.empty-state { grid-column: 1/-1; text-align: center; padding: 40px; background: #1a1f2e; border: 1px dashed #2a2f3e; border-radius: 8px; }
.muted { color: #5a6a7a; }

.type-selector { display: flex; gap: 8px; }
.capability-selector { flex-wrap: wrap; }
.type-btn {
  flex: 1; padding: 10px 16px; background: #0f1219; border: 2px solid #2a2f3e;
  border-radius: 6px; color: #8899aa; font-size: 14px; font-weight: 600;
  cursor: pointer; font-family: inherit; transition: all 0.15s ease;
}
.type-btn:hover { border-color: #3a4f5e; color: #ccd6e0; }
.type-btn.active { border-color: #4fc3f7; color: #4fc3f7; background: rgba(79,195,247,0.08); }
.capability-toggle { display: inline-flex; align-items: center; justify-content: center; gap: 8px; }
.capability-toggle input { width: 16px; height: 16px; margin: 0; }

/* Terminal Preset Cards */
.pc-preset-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 16px;
}
.tp-presets-list { display: flex; flex-direction: column; gap: 16px; }
.tp-provider-group { display: flex; flex-direction: column; gap: 8px; }
.tp-provider-group-header {
  display: flex; align-items: center; gap: 10px;
  padding: 10px 16px;
  background: rgba(79, 195, 247, 0.06);
  border: 1px solid rgba(79, 195, 247, 0.15);
  border-radius: 8px;
}
.tp-provider-group-name {
  font-size: 14px; font-weight: 600; color: var(--accent);
}
.tp-provider-group-badge {
  font-size: 11px; font-weight: 600;
  background: rgba(79, 195, 247, 0.15); color: var(--accent);
  padding: 2px 8px; border-radius: 10px; min-width: 20px; text-align: center;
}
.tp-preset-card { padding: 14px 16px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; transition: border-color 0.15s, box-shadow 0.15s; }
.tp-preset-card:hover { border-color: var(--accent); box-shadow: 0 2px 8px rgba(0,0,0,0.15); }
.tp-preset-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; gap: 8px; }
.tp-preset-header > div { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; }
.tp-preset-name { font-size: 14px; font-weight: 600; color: var(--text-primary); }
.tp-preset-provider { font-size: 12px; color: var(--accent); background: rgba(79,195,247,0.1); padding: 2px 8px; border-radius: 10px; white-space: nowrap; }
.tp-preset-actions { display: flex; gap: 4px; }
.tp-preset-body { display: flex; flex-wrap: wrap; gap: 6px; }
.tp-preset-body .param-badge { background: rgba(90,106,122,0.2); color: var(--text-secondary); padding: 3px 8px; border-radius: 4px; font-size: 11px; border: 1px solid var(--border); }
.tp-section-divider { height: 1px; background: var(--border); margin: 12px 0; }
.tp-compat-warning { margin: 8px 0 0; font-size: 12px; color: var(--error); line-height: 1.4; }

/* OpenCode Preset Management Cards */
.oc-preset-search {
  display: flex; align-items: center; gap: 8px;
  margin-bottom: 16px; padding: 0 2px; color: var(--text-muted);
}
.oc-preset-search-input {
  background: transparent !important; border: none !important;
  padding: 6px 8px !important; font-size: 13px !important; color: var(--text-primary) !important;
}
.oc-preset-search-input::placeholder { color: var(--text-muted); }
.oc-preset-manage-card { padding: 16px !important; }
.oc-preset-manage-header {
  display: flex; justify-content: space-between; align-items: flex-start; gap: 12px;
}
.oc-preset-manage-info {
  display: flex; flex-direction: column; gap: 4px; min-width: 0; flex: 1;
}
.oc-preset-manage-name {
  font-size: 15px; font-weight: 600; color: var(--text-primary);
}
.oc-preset-manage-desc {
  font-size: 13px; color: var(--text-secondary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.oc-preset-manage-meta {
  display: flex; align-items: center; gap: 10px; flex-shrink: 0;
}
.oc-preset-manage-badge {
  font-size: 11px; font-weight: 600;
  background: rgba(90,106,122,0.15); color: var(--text-secondary);
  padding: 3px 10px; border-radius: 10px;
}
.oc-preset-search-empty {
  text-align: center; padding: 20px; color: var(--text-muted); font-size: 13px;
}

/* OpenCode Preset Binding */
.oc-preset-binding-row {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 10px 12px;
  margin-bottom: 8px;
}

/* Buttons */
.btn {
  padding: 10px 20px; border-radius: 6px; font-family: inherit; font-size: 14px; font-weight: 600;
  cursor: pointer; transition: transform 0.15s, box-shadow 0.15s, background 0.15s;
  border: none; outline: none; background: var(--surface); color: var(--text-primary); border: 1px solid var(--border);
}
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn.small { padding: 6px 14px; font-size: 13px; }
.btn.primary { background: var(--accent); color: var(--bg); border-color: transparent; }
.btn.primary:hover:not(:disabled) { background: var(--accent-hover); transform: translateY(-1px); box-shadow: 0 4px 12px rgba(79,195,247,0.2); }
.btn.secondary { background: transparent; color: var(--text-primary); border: 1px solid var(--border); }
.btn.secondary:hover:not(:disabled) { border-color: #5a6a7a; background: rgba(255,255,255,0.05); }
.btn.danger { background: transparent; color: var(--error); border-color: var(--error); }
.btn.danger:hover:not(:disabled) { background: rgba(239,83,80,0.1); }
.btn-icon { background: transparent; border: none; cursor: pointer; padding: 4px; border-radius: 4px; display: flex; align-items: center; justify-content: center; transition: all 0.15s ease; color: #8899aa; }
.btn-icon:hover { background: rgba(255,255,255,0.1); color: #e0e6ed; }
.btn-icon.danger:hover { background: rgba(239,83,80,0.1); color: #ef5350; }

/* Dialog */
.dialog-overlay {
  position: fixed; top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(15,18,25,0.8); display: flex; align-items: center; justify-content: center;
  z-index: 100; backdrop-filter: blur(4px);
}
.dialog { width: 100%; max-width: 480px; box-shadow: 0 8px 32px rgba(0,0,0,0.4); }
.dialog h2 { margin: 0 0 20px 0; color: #e0e6ed; }
.form-group { margin-bottom: 16px; }
.form-group label { display: block; margin-bottom: 8px; color: #8899aa; font-size: 14px; }
.input-field {
  width: 100%; background: #0f1219; border: 1px solid #2a2f3e; color: #e0e6ed;
  padding: 10px 12px; border-radius: 6px; font-family: inherit; font-size: 14px;
  transition: border-color 0.15s; outline: none; box-sizing: border-box;
}
.input-field:focus { border-color: #4fc3f7; }
.monospace { font-family: 'Consolas', 'Monaco', monospace; }
.flex-1 { flex: 1; }
.dialog-scroll-area { max-height: 60vh; overflow-y: auto; padding-right: 8px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 12px; margin-top: 24px; }
.form-row { display: flex; gap: 24px; }
.form-grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }

/* OpenCode Config Styles */
.oc-mode-switch {
  display: inline-flex; background: var(--surface); border: 1px solid var(--border);
  border-radius: 8px; padding: 3px; gap: 2px; flex-shrink: 0;
}
.oc-mode-btn {
  padding: 6px 16px; background: transparent; border: none; border-radius: 6px;
  color: var(--text-secondary); font-size: 13px; font-weight: 500; font-family: inherit;
  cursor: pointer; transition: all 0.15s ease;
}
.oc-mode-btn:hover { color: var(--text-primary); }
.oc-mode-btn.active { background: var(--accent); color: var(--bg); font-weight: 600; }

.oc-status-bar { display: flex; align-items: center; gap: 12px; margin-top: 8px; min-height: 24px; }
.opencode-unsaved-badge {
  display: inline-block; padding: 3px 10px; background: rgba(79,195,247,0.1);
  color: var(--accent); border-radius: 12px; font-size: 11px; font-weight: 600;
}
.oc-validation { font-size: 12px; font-weight: 500; }
.oc-validation.neutral { color: var(--text-muted); }
.oc-validation.valid { color: var(--success); }
.oc-validation.invalid { color: var(--error); }
.oc-switch-warning { font-size: 12px; color: var(--error); font-weight: 500; }

.group-separator { height: 1px; background: var(--border); margin: 24px 0; }
.inline-input-group { display: flex; align-items: center; gap: 8px; }

.oc-visual-mode { display: flex; flex-direction: column; gap: 4px; }
.oc-section { border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
.oc-section:hover { border-color: var(--border-hover); }
.oc-section-header {
  padding: 12px 18px; cursor: pointer; display: flex; align-items: center; gap: 8px;
  font-size: 14px; font-weight: 500; color: var(--text-secondary); background: var(--surface);
  transition: background 0.12s, color 0.12s; user-select: none;
}
.oc-section-header:hover { background: rgba(79,195,247,0.04); color: var(--text-primary); }
.oc-collapse-icon { font-size: 10px; color: var(--accent); width: 14px; text-align: center; }
.oc-count-badge { font-size: 11px; font-weight: 600; background: rgba(79,195,247,0.15); color: var(--accent); padding: 1px 7px; border-radius: 10px; margin-left: 4px; }
.oc-section-body { padding: 12px 18px 18px; display: flex; flex-direction: column; gap: 12px; }
.oc-card { background: var(--bg); border: 1px solid var(--border); border-radius: 6px; padding: 14px; display: flex; flex-direction: column; gap: 12px; }
.oc-sub-card { background: rgba(255,255,255,0.02); border: 1px solid var(--border); border-radius: 6px; padding: 12px; display: flex; flex-direction: column; gap: 10px; margin-bottom: 10px; }
.oc-card-header { display: flex; align-items: center; justify-content: space-between; }
.oc-card-name { font-weight: 600; font-size: 13px; color: var(--accent); font-family: 'Consolas', 'Monaco', monospace; }
.oc-remove-btn { background: transparent; border: none; cursor: pointer; padding: 4px 6px; border-radius: 4px; color: var(--text-muted); font-size: 13px; line-height: 1; transition: all 0.15s ease; }
.oc-remove-btn:hover { background: rgba(239,83,80,0.1); color: var(--error); }
.oc-kv-row { display: flex; gap: 8px; align-items: center; margin-bottom: 8px; }
.oc-kv-row .input-field { flex: 1; }
.oc-kv-key { flex: 2; }
.oc-kv-value { flex: 1; }
.oc-color-row { display: flex; align-items: center; gap: 10px; }
.oc-color-picker { width: 44px; height: 36px; padding: 4px; border: 1px solid var(--border); border-radius: 6px; background: var(--bg); cursor: pointer; }
.oc-color-preview { width: 36px; height: 36px; border-radius: 6px; border: 1px solid var(--border); background: linear-gradient(135deg, rgba(255,255,255,0.08), rgba(255,255,255,0.02)); flex-shrink: 0; }
.oc-color-preview.invalid { background: repeating-linear-gradient(45deg, rgba(255,255,255,0.04), rgba(255,255,255,0.04) 6px, rgba(255,255,255,0.1) 6px, rgba(255,255,255,0.1) 12px); }

.opencode-editor {
  min-height: 380px; max-height: 60vh; resize: vertical; padding: 16px;
  background: var(--bg); color: var(--text-primary); border: 1px solid var(--border);
  border-radius: 8px; font-family: 'Consolas', 'Monaco', monospace; font-size: 13px;
  line-height: 1.6; tab-size: 2; white-space: pre; overflow: auto; outline: none;
}
.opencode-editor:focus { border-color: var(--accent); }
.oc-error-detail { margin-top: 8px; padding: 8px 12px; background: rgba(239,83,80,0.08); border: 1px solid rgba(239,83,80,0.2); border-radius: 6px; color: #ef9a9a; font-size: 12px; }

.opencode-actions { display: flex; align-items: center; gap: 8px; margin-top: 16px; }
.opencode-actions-spacer { flex: 1; }

.oc-sub-error-banner { display: flex; align-items: flex-start; gap: 12px; padding: 12px 16px; background: rgba(239,83,80,0.06); border: 1px solid rgba(239,83,80,0.2); border-radius: 8px; margin-bottom: 8px; }
.oc-sub-error-icon { display: flex; align-items: center; justify-content: center; width: 20px; height: 20px; border-radius: 50%; background: rgba(239,83,80,0.15); color: #ef5350; font-size: 12px; font-weight: 700; flex-shrink: 0; font-style: normal; }
.oc-sub-error-content { flex: 1; min-width: 0; }
.oc-sub-error-title { color: #ef9a9a; font-size: 13px; font-weight: 500; margin-bottom: 8px; }
.oc-sub-error-item { font-size: 12px; color: #ef9a9a; padding: 3px 8px; background: rgba(239,83,80,0.08); border-radius: 3px; margin-bottom: 4px; word-break: break-all; }
.oc-sub-error-field { color: #ef5350; font-weight: 600; }

.select-wrapper { position: relative; }
</style>
