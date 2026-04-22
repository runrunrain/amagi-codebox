<template>
  <div class="settings-layout">
    <!-- 左侧导航 -->
    <div class="settings-sidebar">
      <h1 class="page-title">设置</h1>
      <nav class="nav-tabs">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          class="nav-tab"
          :class="{ active: activeTab === tab.id }"
          @click="activeTab = tab.id"
        >
          <span class="tab-icon">{{ tab.icon }}</span>
          <span class="tab-label">{{ tab.label }}</span>
        </button>
      </nav>
    </div>

    <!-- 右侧内容 -->
    <div class="settings-content-wrapper">
      <transition name="fade-slide" mode="out-in">
        
        <!-- 常规设置 -->
        <div v-if="activeTab === 'general'" key="general" class="settings-section">
          <div class="section-header">
            <h2>仪表盘默认配置</h2>
            <p>配置仪表盘各选项的初始默认值，下次启动应用时生效。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">服务提供商</h3>
            <div class="form-row">
              <div class="form-group flex-1">
                <label>默认服务提供商</label>
                <div class="select-wrapper">
                  <select v-model="defaults.provider" class="input-field">
                    <option value="">（不指定）</option>
                    <option v-for="(_, name) in anthropicProviders" :key="name" :value="name">{{ name }}</option>
                  </select>
                </div>
              </div>
              <div class="form-group flex-1">
                <label>默认预设配置</label>
                <div class="select-wrapper">
                  <select v-model="defaults.preset" class="input-field" :disabled="!defaults.provider">
                    <option value="">（不指定）</option>
                    <option v-for="(preset, name) in availablePresets" :key="name" :value="name">
                      {{ name }} ({{ preset.model }})
                    </option>
                  </select>
                </div>
              </div>
            </div>
            
            <div class="form-row" style="margin-top: 8px;">
              <div class="form-group flex-1">
                <label>默认 OpenCode 服务提供商</label>
                <div class="select-wrapper">
                  <select v-model="defaults.openCodeProvider" class="input-field">
                    <option value="">（不指定，沿用本机 OpenCode 登录）</option>
                    <option v-for="(_, name) in openCodeProviders" :key="name" :value="name">{{ name }}</option>
                  </select>
                </div>
              </div>
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">引擎默认配置</h3>
            <div class="engine-tabs">
              <button 
                v-for="eng in engines" 
                :key="eng.id" 
                class="engine-tab"
                :class="{ active: activeEngineTab === eng.id }"
                @click="activeEngineTab = eng.id"
              >{{ eng.label }}</button>
            </div>

            <div class="engine-content">
              <div class="form-group">
                <label>启动模式</label>
                <div class="mode-selector">
                  <button
                    v-for="m in launchModes"
                    :key="m.value"
                    class="mode-btn"
                    :class="{ active: currentEngineMode === m.value }"
                    @click="currentEngineMode = m.value"
                  >
                    <span class="mode-icon">{{ m.icon }}</span>
                    <span class="mode-label">{{ m.label }}</span>
                  </button>
                </div>
              </div>
              <div class="form-group" style="margin-top: 24px;">
                <label>默认 Shell</label>
                <div class="shell-pills">
                  <button
                    v-for="s in shellOptions"
                    :key="s.value"
                    class="shell-pill"
                    :class="{ active: currentEngineShell === s.value }"
                    @click="currentEngineShell = s.value"
                  >
                    {{ s.label }}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">网络</h3>
            <div class="toggle-row">
              <div class="toggle-info">
                <label>默认启用注入代理</label>
                <span class="field-desc">自动设置环境变量以代理请求</span>
              </div>
              <button 
                class="ios-toggle" 
                :class="{ active: defaults.useProxy }"
                @click="defaults.useProxy = !defaults.useProxy"
              ></button>
            </div>
          </div>

          <div class="section-footer">
            <button class="btn primary" @click="saveDefaults" :disabled="saving">
              {{ saving ? '保存中...' : '保存默认配置' }}
            </button>
          </div>
        </div>

        <!-- 自定义 Shell -->
        <div v-if="activeTab === 'shell'" key="shell" class="settings-section">
          <div class="section-header">
            <h2>自定义 Shell 路径</h2>
            <p>添加自定义 Shell 可执行文件路径，在仪表盘中可快速切换。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">添加新 Shell</h3>
            <div class="add-shell-card">
              <input type="text" class="input-field" v-model="newShellLabel" placeholder="名称（如 Git Bash）" style="width: 180px;" />
              <input type="text" class="input-field flex-1" v-model="newShellPath" placeholder="Shell 可执行文件路径" />
              <button class="btn primary" @click="addShell" :disabled="!newShellPath">添加</button>
            </div>
          </div>

          <div class="setting-group" style="margin-top: 32px;">
            <h3 class="group-header">已保存的路径</h3>
            <div class="shell-list" v-if="shellPaths.length > 0">
              <div class="shell-list-item" v-for="entry in shellPaths" :key="entry.path">
                <div class="shell-info">
                  <span class="shell-label">{{ entry.label }}</span>
                  <span class="shell-path">{{ entry.path }}</span>
                </div>
                <button class="btn small danger delete-btn" @click="removeShell(entry.path)">删除</button>
              </div>
            </div>
            <div class="empty-state" v-else>
              <span>暂无自定义 Shell 路径</span>
            </div>
          </div>
        </div>

        <!-- 终端设置 -->
        <div v-if="activeTab === 'terminal'" key="terminal" class="settings-section">
          <div class="section-header">
            <h2>终端设置</h2>
            <p>配置内嵌终端的显示参数与行为。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">滚动缓冲</h3>
            <div class="form-group">
              <label>缓冲行数 (Scrollback)</label>
              <div class="range-with-input" style="margin-top: 12px;">
                <input type="range" class="range-slider flex-1" v-model.number="terminalScrollback" min="1000" max="10000000" step="10000" />
                <input type="number" class="input-field" v-model.number="terminalScrollback" style="width: 140px;" min="1000" max="10000000" step="10000" />
              </div>
              <p class="field-desc" style="margin-top: 12px;">保留在内存中的终端输出行数。范围 1,000 ~ 10,000,000。较高值可能占用更多内存。</p>
            </div>
          </div>

          <div class="section-footer">
            <button class="btn primary" @click="saveTerminal" :disabled="savingTerminal">
              {{ savingTerminal ? '保存中...' : '保存终端设置' }}
            </button>
          </div>
        </div>

        <!-- OpenCode 全局配置 -->
        <div v-if="activeTab === 'opencode'" key="opencode" class="settings-section">
          <div class="section-header">
            <div class="oc-header-row">
              <div>
                <h2>OpenCode 全局配置</h2>
                <p>编辑全局 opencode.json 配置文件。修改后保存立即生效。</p>
              </div>
              <div class="oc-mode-switch">
                <button
                  class="oc-mode-btn"
                  :class="{ active: ocEditMode === 'visual' }"
                  @click="ocSwitchToVisual"
                >可视化</button>
                <button
                  class="oc-mode-btn"
                  :class="{ active: ocEditMode === 'json' }"
                  @click="ocSwitchToJson"
                >JSON</button>
              </div>
            </div>
          </div>

          <div class="setting-group">
            <h3 class="group-header">配置文件路径</h3>
            <div class="inline-input-group">
              <input type="text" class="input-field monospace opencode-path flex-1" :value="ocConfigPath" readonly />
              <button class="btn small" @click="copyConfigPath">复制路径</button>
            </div>
          </div>

          <div class="opencode-notice" v-if="ocHasSensitiveHint">
            <span class="notice-icon">!</span>
            <span>此文件可能包含 API Key 等敏感信息，编辑时请留意。</span>
          </div>

          <div class="oc-status-bar">
            <span v-if="ocHasUnsavedChanges" class="opencode-unsaved-badge">未保存的更改</span>
            <span class="oc-validation" :class="ocValidationClass">{{ ocValidationText }}</span>
            <span v-if="ocSwitchBlocked" class="oc-switch-warning">JSON 非法，无法切换模式</span>
            <span v-if="ocHasSubJsonErrors && ocEditMode === 'visual'" class="oc-switch-warning">部分 JSON 字段格式有误，请修正后再保存或切换模式</span>
          </div>

          <div class="group-separator"></div>

          <!-- ========== VISUAL MODE ========== -->
          <div v-if="ocEditMode === 'visual'" class="oc-visual-mode">

            <!-- $schema -->
            <div class="oc-section" v-if="ocSchemaValue">
              <div class="oc-section-header" @click="ocToggleSection('schema')">
                <span class="oc-collapse-icon">{{ ocSections.schema ? '&#9660;' : '&#9654;' }}</span>
                <span>$schema</span>
              </div>
              <div class="oc-section-body" v-if="ocSections.schema">
                <div class="form-group">
                  <label>Schema URI</label>
                  <input type="text" v-model="ocGui.schemaValue" class="input-field monospace" placeholder="https://opencode.ai/config.json" @input="ocGuiToRaw" />
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
                <p class="field-desc" style="margin-bottom: 12px;">每个 provider 条目可包含 options (apiKey/baseURL)、models、npm、name 等字段。</p>
                <div v-for="(prov, idx) in ocGui.providers" :key="idx" class="oc-card">
                  <div class="oc-card-header">
                    <span class="oc-card-name">{{ prov.name || '(unnamed)' }}</span>
                    <button class="oc-remove-btn" @click="ocRemoveProvider(idx)" title="删除">&#10005;</button>
                  </div>
                  <div class="form-group">
                    <label>Provider ID</label>
                    <input type="text" v-model="prov.name" class="input-field" placeholder="anthropic, openai, github-copilot..." @input="ocGuiToRaw" />
                  </div>
                  <div class="form-row">
                    <div class="form-group flex-1">
                      <label>API Key (options.apiKey)</label>
                      <input type="password" v-model="prov.apiKey" class="input-field monospace" placeholder="sk-..." @input="ocGuiToRaw" />
                    </div>
                    <div class="form-group flex-1">
                      <label>Base URL (options.baseURL)</label>
                      <input type="text" v-model="prov.baseURL" class="input-field monospace" placeholder="https://api.anthropic.com" @input="ocGuiToRaw" />
                    </div>
                  </div>
                  <div class="form-group">
                    <label>Options 额外字段 (JSON, 不含 apiKey/baseURL)</label>
                    <textarea v-model="prov.optionsExtraRaw" class="input-field monospace oc-mini-textarea" rows="3" placeholder='{"store": true, "thinking": {"type": "enabled", "budgetTokens": 10000}, "enable_search": true}' @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`provider.${prov.name}.optionsExtra`]">JSON 格式错误: {{ ocSubJsonErrors[`provider.${prov.name}.optionsExtra`] }}</span>
                    <span class="field-desc">provider.options 内除 apiKey/baseURL 外的其它字段，如 store, thinking, enable_thinking 等</span>
                  </div>
                  <div class="form-group">
                    <label>Models (JSON 对象)</label>
                    <textarea v-model="prov.modelsRaw" class="input-field monospace oc-mini-textarea" rows="4" placeholder='{ "claude-opus-4-6": { "name": "Claude Opus 4.6", "variants": {} } }' @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`provider.${prov.name}.models`]">JSON 格式错误: {{ ocSubJsonErrors[`provider.${prov.name}.models`] }}</span>
                    <span class="field-desc">每个 model 可含 name, options, variants 等子字段</span>
                  </div>
                  <div class="form-group">
                    <label>额外字段 (JSON, 不含 options/models)</label>
                    <textarea v-model="prov.extraRaw" class="input-field monospace oc-mini-textarea" rows="2" placeholder='{"npm": "@ai-sdk/anthropic", "name": "..."}' @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`provider.${prov.name}.extra`]">JSON 格式错误: {{ ocSubJsonErrors[`provider.${prov.name}.extra`] }}</span>
                  </div>
                </div>
                <button class="btn small" @click="ocAddProvider">+ 添加 Provider</button>
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
                    <div class="form-group flex-1">
                      <label>名称</label>
                      <input type="text" v-model="agent.name" class="input-field" placeholder="coder, my-agent..." @input="ocGuiToRaw" />
                    </div>
                    <div class="form-group" style="width: 160px;">
                      <label>Mode</label>
                      <div class="select-wrapper">
                        <select v-model="agent.mode" class="input-field" @change="ocGuiToRaw">
                          <option value="primary">primary</option>
                          <option value="subagent">subagent</option>
                        </select>
                      </div>
                    </div>
                  </div>
                  <div class="form-row">
                    <div class="form-group flex-1">
                      <label>Model (provider/model 格式)</label>
                      <input type="text" v-model="agent.model" class="input-field monospace" placeholder="anthropic/claude-sonnet-4-6" @input="ocGuiToRaw" />
                    </div>
                    <div class="form-group" style="width: 120px;">
                      <label>Color</label>
                      <input type="text" v-model="agent.color" class="input-field monospace" placeholder="#FF69B4" @input="ocGuiToRaw" />
                    </div>
                  </div>
                  <div class="form-group">
                    <label>Description</label>
                    <input type="text" v-model="agent.description" class="input-field" placeholder="Agent 的简短描述" @input="ocGuiToRaw" />
                  </div>
                  <div class="form-group">
                    <label>Prompt (系统指令)</label>
                    <textarea v-model="agent.prompt" class="input-field" rows="3" placeholder="Agent 的系统提示词" @input="ocGuiToRaw"></textarea>
                  </div>
                  <div class="form-group">
                    <label>Tools 黑名单 (JSON, true=禁用)</label>
                    <textarea v-model="agent.toolsRaw" class="input-field monospace oc-mini-textarea" rows="2" placeholder='{"webfetch": false, "apply_patch": false}' @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`agent.${agent.name}.tools`]">JSON 格式错误: {{ ocSubJsonErrors[`agent.${agent.name}.tools`] }}</span>
                  </div>
                  <div class="form-group">
                    <label>额外字段 (JSON)</label>
                    <textarea v-model="agent.extraRaw" class="input-field monospace oc-mini-textarea" rows="2" @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`agent.${agent.name}.extra`]">JSON 格式错误: {{ ocSubJsonErrors[`agent.${agent.name}.extra`] }}</span>
                  </div>
                </div>
                <button class="btn small" @click="ocAddAgent">+ 添加 Agent</button>
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
                      <input type="text" v-model="mcp.name" class="input-field" placeholder="my-mcp-server" @input="ocGuiToRaw" />
                    </div>
                    <div class="form-group" style="width: 140px;">
                      <label>Type</label>
                      <div class="select-wrapper">
                        <select v-model="mcp.type" class="input-field" @change="ocGuiToRaw">
                          <option value="remote">remote</option>
                          <option value="local">local</option>
                        </select>
                      </div>
                    </div>
                  </div>
                  <div class="form-group" v-if="mcp.type === 'remote'">
                    <label>URL</label>
                    <input type="text" v-model="mcp.url" class="input-field" placeholder="https://..." @input="ocGuiToRaw" />
                  </div>
                  <div class="form-group" v-if="mcp.type === 'local'">
                    <label>Command (JSON 数组)</label>
                    <input type="text" v-model="mcp.commandRaw" class="input-field monospace" placeholder='["uvx", "my-mcp-server"]' @input="ocGuiToRaw" />
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`mcp.${mcp.name}.command`]">JSON 格式错误: {{ ocSubJsonErrors[`mcp.${mcp.name}.command`] }}</span>
                  </div>
                  <div class="form-group">
                    <label>Headers (JSON)</label>
                    <textarea v-model="mcp.headersRaw" class="input-field monospace oc-mini-textarea" rows="2" placeholder='{"Authorization": "Bearer ..."}' @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`mcp.${mcp.name}.headers`]">JSON 格式错误: {{ ocSubJsonErrors[`mcp.${mcp.name}.headers`] }}</span>
                  </div>
                  <div class="form-group">
                    <label>Environment (JSON)</label>
                    <textarea v-model="mcp.environmentRaw" class="input-field monospace oc-mini-textarea" rows="2" placeholder='{"API_KEY": "{env:MY_KEY}"}' @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`mcp.${mcp.name}.environment`]">JSON 格式错误: {{ ocSubJsonErrors[`mcp.${mcp.name}.environment`] }}</span>
                  </div>
                  <div class="toggle-row" style="padding: 8px 0;">
                    <div class="toggle-info">
                      <label>OAuth</label>
                    </div>
                    <button class="ios-toggle" :class="{ active: mcp.oauth }" @click="mcp.oauth = !mcp.oauth; ocGuiToRaw()"></button>
                  </div>
                  <div class="form-group">
                    <label>额外字段 (JSON)</label>
                    <textarea v-model="mcp.extraRaw" class="input-field monospace oc-mini-textarea" rows="2" @input="ocGuiToRaw"></textarea>
                    <span class="oc-sub-error" v-if="ocSubJsonErrors[`mcp.${mcp.name}.extra`]">JSON 格式错误: {{ ocSubJsonErrors[`mcp.${mcp.name}.extra`] }}</span>
                  </div>
                </div>
                <button class="btn small" @click="ocAddMcp">+ 添加 MCP Server</button>
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
                  <div class="select-wrapper">
                    <select v-model="perm.value" class="input-field oc-kv-value" @change="ocGuiToRaw">
                      <option value="allow">allow</option>
                      <option value="deny">deny</option>
                      <option value="ask">ask</option>
                    </select>
                  </div>
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
                  <input type="text" v-model="ocGui.plugins[idx]" class="input-field" placeholder="插件名称或路径" @input="ocGuiToRaw" />
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
                  <input type="text" v-model="kv.key" class="input-field oc-kv-key" placeholder="key" @input="ocGuiToRaw" />
                  <input type="text" v-model="kv.valueRaw" class="input-field oc-kv-value" placeholder="true / 15000 / string" @input="ocGuiToRaw" />
                  <button class="oc-remove-btn" @click="ocRemoveExperimental(idx)" title="删除">&#10005;</button>
                </div>
                <button class="btn small" @click="ocAddExperimental">+ 添加 Experimental</button>
              </div>
            </div>

            <!-- Unknown / Extra fields -->
            <div class="oc-section" v-if="ocGui.unknownFieldsRaw || ocShowExtraSection">
              <div class="oc-section-header" @click="ocToggleSection('extra')">
                <span class="oc-collapse-icon">{{ ocSections.extra ? '&#9660;' : '&#9654;' }}</span>
                <span>高级 / 未识别字段 <span class="oc-count-badge" v-if="ocGui.unknownFieldsRaw">有</span></span>
              </div>
              <div class="oc-section-body" v-if="ocSections.extra">
                <p class="field-desc" style="margin-bottom: 8px;">以下字段未被上方结构化面板覆盖，以原始 JSON 保留。直接编辑可保真所有数据。</p>
                <textarea
                  v-model="ocGui.unknownFieldsRaw"
                  class="input-field monospace oc-mini-textarea"
                  rows="6"
                  placeholder="{}"
                  @input="ocGuiToRaw"
                ></textarea>
                <span class="oc-sub-error" v-if="ocSubJsonErrors['unknownFields']">JSON 格式错误: {{ ocSubJsonErrors['unknownFields'] }}</span>
              </div>
            </div>

          </div>

          <!-- ========== JSON MODE ========== -->
          <div v-if="ocEditMode === 'json'" class="setting-group">
            <div class="opencode-editor-header">
              <h3 class="group-header" style="margin-bottom:0;">JSON 编辑器</h3>
            </div>

            <div class="opencode-editor-wrap">
              <textarea
                ref="ocEditorRef"
                class="input-field monospace opencode-editor"
                v-model="ocEditorContent"
                spellcheck="false"
                autocomplete="off"
                autocorrect="off"
                autocapitalize="off"
                placeholder="{ }"
                @keydown.tab.prevent="handleTabKey"
              ></textarea>
            </div>

            <div v-if="ocValidationError && ocValidationError !== ''" class="oc-error-detail">
              {{ ocValidationError }}
            </div>
          </div>

          <div class="opencode-actions">
            <button class="btn small" @click="ocReload" :disabled="ocReloading">
              {{ ocReloading ? '加载中...' : '重新加载' }}
            </button>
            <button class="btn small" @click="ocFormat" :disabled="!ocIsParseableJson">
              格式化
            </button>
            <button class="btn small danger" @click="ocRevert" :disabled="!ocHasUnsavedChanges || ocReverting">
              {{ ocReverting ? '恢复中...' : '恢复到磁盘' }}
            </button>
            <div class="opencode-actions-spacer"></div>
            <button class="btn primary" @click="ocSave" :disabled="!ocCanSave || ocSaving">
              {{ ocSaving ? '保存中...' : '保存' }}
            </button>
          </div>
        </div>

        <!-- 远程控制 -->
        <div v-if="activeTab === 'remote'" key="remote" class="settings-section">
          <div class="section-header">
            <h2>远程控制</h2>
            <p>允许移动端通过局域网连接并控制 Amagi CodeBox。</p>
          </div>

          <div class="remote-hero">
            <div class="remote-status" :class="{ active: remoteEnabled }">
              <div class="status-ring"></div>
              <div class="status-info">
                <h4>{{ remoteEnabled ? '服务运行中' : '服务已停止' }}</h4>
                <p>{{ remoteEnabled ? `正在监听 ${remoteStatus.host || '0.0.0.0'}:${remoteStatus.port}` : '启用以允许外部连接' }}</p>
              </div>
            </div>
            <button 
              class="ios-toggle large-toggle" 
              :class="{ active: remoteEnabled }"
              @click="toggleRemote"
              :disabled="togglingRemote"
            ></button>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">连接设置</h3>
            <div class="form-row">
              <div class="form-group" style="flex: 1;">
                <label>监听地址</label>
                <div class="inline-input-group">
                  <input type="text" class="input-field" v-model="remoteHost" placeholder="0.0.0.0" />
                </div>
              </div>
              <div class="form-group" style="width: 180px;">
                <label>监听端口</label>
                <div class="inline-input-group">
                  <input type="number" class="input-field" v-model.number="remotePort" min="1024" max="65535" />
                </div>
              </div>
              <div class="form-group" style="align-self: flex-end;">
                <button class="btn primary small" @click="applyHostPort" :disabled="savingPort">应用</button>
              </div>
            </div>

            <div class="form-group">
              <label>访问 Token</label>
              <div class="inline-input-group">
                <input :type="showToken ? 'text' : 'password'" class="input-field monospace token-input flex-1" :value="remoteToken" readonly />
                <button class="btn small" @click="showToken = !showToken">{{ showToken ? '隐藏' : '显示' }}</button>
                <button class="btn small" @click="copyToken">复制</button>
                <button class="btn small danger" @click="regenerateToken" :disabled="regenerating">刷新</button>
              </div>
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">移动端 Web 资源</h3>
            <div class="form-group">
              <label>前端构建目录</label>
              <p class="field-desc" style="margin-bottom: 8px;">指向 amagi-codebox-mobile 的 dist 目录。配置后可在同一端口直接访问移动端页面。</p>
              <div class="inline-input-group">
                <input type="text" class="input-field flex-1" v-model="mobileWebRoot" placeholder="例如：C:\projects\amagi-codebox-mobile\dist" />
                <button class="btn primary small" @click="saveMobileWebRoot" :disabled="savingWebRoot">保存</button>
              </div>
            </div>
          </div>

          <transition name="fade-slide">
            <div v-if="remoteEnabled" class="qr-section">
              <div class="group-separator"></div>
              <h3 class="group-header">快速连接</h3>
              <div class="qr-frame">
                <canvas ref="qrCanvas" class="qr-canvas"></canvas>
                <p>使用移动端扫描二维码快速建立连接</p>
              </div>
            </div>
          </transition>
        </div>

        <!-- 软件更新 -->
        <div v-if="activeTab === 'updates'" key="updates" class="settings-section">
          <div class="section-header">
            <h2>软件更新</h2>
            <p>检查并安装来自 GitHub Releases 的更新。</p>
          </div>

          <div class="setting-group">
            <h3 class="group-header">版本信息</h3>
            <div class="update-hero">
              <div class="version-info">
                <span class="version-label">当前版本</span>
                <span class="version-badge">v{{ currentVersion }}</span>
              </div>
              <button class="btn primary" @click="checkForUpdate" :disabled="checking || downloading">
                {{ checking ? '检查中...' : '检查更新' }}
              </button>
            </div>

            <!-- Update Available Card -->
            <div v-if="updateInfo && updateInfo.hasUpdate" class="update-card">
              <div class="update-card-header">
                <span class="status-dot online"></span>
                <span class="update-title">发现新版本</span>
                <span class="update-version-new">v{{ updateInfo.latestVersion }}</span>
              </div>
              <p class="update-date">发布于：{{ updateInfo.publishedAt }}</p>
              
              <div class="release-notes" v-if="updateInfo.releaseNotes">
                <pre>{{ updateInfo.releaseNotes }}</pre>
              </div>

              <div class="update-actions">
                <button class="btn primary" @click="downloadAndApply" :disabled="downloading">
                  {{ downloading ? '下载中...' : '下载并安装' }}
                </button>
              </div>

              <div v-if="downloading" class="progress-container">
                <div class="progress-bar">
                  <div class="progress-fill" :style="{ width: progressPercent + '%' }"></div>
                </div>
                <span class="progress-text">{{ progressText }}</span>
              </div>
            </div>

            <!-- Up to date -->
            <div v-else-if="updateInfo && !updateInfo.hasUpdate" class="update-uptodate">
              <span class="status-dot online"></span>
              <span>当前已是最新版本</span>
            </div>

            <div v-if="updateError" class="update-error">
              {{ updateError }}
            </div>
          </div>

          <div class="group-separator"></div>

          <div class="setting-group">
            <h3 class="group-header">GitHub 授权</h3>
            <div class="form-group">
              <label>Personal Access Token</label>
              <p class="field-desc" style="margin-bottom: 8px;">获取私有仓库的 Releases 需要配置含有 repo 权限的 Token。</p>
              <div class="inline-input-group">
                <input
                  :type="showGHToken ? 'text' : 'password'"
                  class="input-field flex-1"
                  v-model="githubToken"
                  placeholder="ghp_xxxxxxxxxxxx"
                />
                <button class="btn small" @click="showGHToken = !showGHToken">
                  {{ showGHToken ? '隐藏' : '显示' }}
                </button>
                <button class="btn primary small" @click="saveGitHubToken" :disabled="savingGHToken">
                  {{ savingGHToken ? '保存中...' : '保存' }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- 关于 -->
        <div v-if="activeTab === 'about'" key="about" class="settings-section">
          <div class="about-container">
            <div class="app-logo">
              <span class="app-icon">▨</span>
            </div>
            <h2 class="app-name">Amagi CodeBox</h2>
            <p class="app-version">Version {{ currentVersion }}</p>
            
            <div class="about-details">
              <div class="detail-row">
                <span class="detail-label">配置目录</span>
                <span class="detail-value monospace">~/.amagi-codebox/</span>
              </div>
            </div>
          </div>
        </div>

      </transition>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, reactive, computed, onMounted, watch, nextTick } from 'vue'
import { GetDashboardDefaults, SetDashboardDefaults, GetShellPaths, AddShellPath, RemoveShellPath, GetTerminalSettings, SetTerminalSettings, GetMobileWebRoot, SetMobileWebRoot } from '../../wailsjs/go/settings/Service'
import { GetProviders } from '../../wailsjs/go/config/ConfigService'
import { GetRemoteStatus, GetRemoteToken, RegenerateRemoteToken, ToggleRemoteServer, SetRemoteHost, SetRemotePort, CheckForUpdate, DownloadAndApplyUpdate, GetAppInfo, GetGitHubToken, SetGitHubToken, GetOpenCodeConfig, SaveOpenCodeConfig, GetOpenCodeConfigPath } from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { config } from '../../wailsjs/go/models'
import { useToast } from '../composables/useToast'
import QRCode from 'qrcode'

const { showSuccess, showError } = useToast()

const activeTab = ref('general')
const tabs = [
  { id: 'general', label: '常规设置', icon: '⚙' },
  { id: 'shell', label: 'Shell', icon: '⌨' },
  { id: 'terminal', label: '终端设置', icon: '🖥' },
  { id: 'opencode', label: 'OpenCode', icon: '⊏' },
  { id: 'remote', label: '远程控制', icon: '🌐' },
  { id: 'updates', label: '软件更新', icon: '⟳' },
  { id: 'about', label: '关于', icon: 'ℹ' },
]

const activeEngineTab = ref('claude')
const engines = [
  { id: 'claude', label: 'ClaudeCode' },
  { id: 'opencode', label: 'OpenCode' },
  { id: 'codex', label: 'Codex' }
]

const providers = ref<Record<string, config.Provider>>({})
const shellPaths = ref<Array<{ path: string; label: string }>>([])
const saving = ref(false)

const defaults = reactive({
  provider: '',
  preset: '',
  openCodeProvider: '',
  mode: 'embedded',
  shell: 'pwsh',
  claudeMode: 'embedded',
  claudeShell: 'pwsh',
  openCodeMode: 'embedded',
  openCodeShell: 'pwsh',
  codexMode: 'embedded',
  codexShell: 'pwsh',
  useProxy: false,
})

const currentEngineMode = computed({
  get: () => {
    if (activeEngineTab.value === 'claude') return defaults.claudeMode;
    if (activeEngineTab.value === 'opencode') return defaults.openCodeMode;
    return defaults.codexMode;
  },
  set: (val) => {
    if (activeEngineTab.value === 'claude') defaults.claudeMode = val;
    else if (activeEngineTab.value === 'opencode') defaults.openCodeMode = val;
    else defaults.codexMode = val;
  }
})

const currentEngineShell = computed({
  get: () => {
    if (activeEngineTab.value === 'claude') return defaults.claudeShell;
    if (activeEngineTab.value === 'opencode') return defaults.openCodeShell;
    return defaults.codexShell;
  },
  set: (val) => {
    if (activeEngineTab.value === 'claude') defaults.claudeShell = val;
    else if (activeEngineTab.value === 'opencode') defaults.openCodeShell = val;
    else defaults.codexShell = val;
  }
})

const newShellLabel = ref('')
const newShellPath = ref('')
const terminalScrollback = ref(100000)
const savingTerminal = ref(false)

const currentVersion = ref('')
const updateInfo = ref<any>(null)
const checking = ref(false)
const downloading = ref(false)
const downloadProgress = ref({ downloaded: 0, total: 0 })
const updateError = ref('')
const githubToken = ref('')
const showGHToken = ref(false)
const savingGHToken = ref(false)

const progressPercent = computed(() => {
  const { downloaded, total } = downloadProgress.value
  if (total <= 0) return 0
  return Math.min(100, Math.round((downloaded / total) * 100))
})

const progressText = computed(() => {
  const { downloaded, total } = downloadProgress.value
  const fmt = (n: number) => (n / 1024 / 1024).toFixed(1) + ' MB'
  if (total <= 0) return '准备中...'
  return `${fmt(downloaded)} / ${fmt(total)}`
})

async function checkForUpdate() {
  checking.value = true
  updateError.value = ''
  updateInfo.value = null
  try {
    const info = await CheckForUpdate()
    updateInfo.value = info
  } catch (err) {
    updateError.value = '检查失败: ' + err
  } finally {
    checking.value = false
  }
}

async function downloadAndApply() {
  downloading.value = true
  downloadProgress.value = { downloaded: 0, total: 0 }
  updateError.value = ''
  try {
    EventsOn('update:progress', (progress: any) => {
      downloadProgress.value = progress
    })
    await DownloadAndApplyUpdate()
  } catch (err) {
    updateError.value = '下载失败: ' + err
    downloading.value = false
  }
}

async function saveGitHubToken() {
  savingGHToken.value = true
  try {
    await SetGitHubToken(githubToken.value.trim())
    showSuccess('GitHub Token 已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    savingGHToken.value = false
  }
}

const launchModes = [
  { value: 'embedded', label: '内嵌终端', icon: '▨' },
  { value: 'terminal', label: '独立窗口', icon: '⬛' },
]

const shellOptions = [
  { value: '', label: '直接 Claude' },
  { value: 'pwsh', label: 'PowerShell 7' },
  { value: 'powershell', label: 'Windows PowerShell' },
  { value: 'cmd', label: 'CMD' },
]

const availablePresets = computed(() => {
  if (!defaults.provider || !providers.value[defaults.provider]) return {}
  return providers.value[defaults.provider].presets || {}
})

const anthropicProviders = computed(() => {
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if ((provider.type || 'anthropic') !== 'openai' && provider.auth_key !== 'OPENAI_API_KEY') {
      result[name] = provider
    }
  }
  return result
})

const openCodeProviders = computed(() => {
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (provider.type === 'openai' || provider.auth_key === 'OPENAI_API_KEY') {
      result[name] = provider
    }
  }
  return result
})

watch(() => defaults.provider, (newVal) => {
  if (newVal && providers.value[newVal]) {
    const presets = providers.value[newVal].presets || {}
    const presetKeys = Object.keys(presets)
    if (presetKeys.length > 0 && !presetKeys.includes(defaults.preset)) {
      defaults.preset = presetKeys[0]
    }
  } else {
    defaults.preset = ''
  }
})

const loadData = async () => {
  try {
    providers.value = await GetProviders()
  } catch (err) {
    console.error('load providers:', err)
  }
  try {
    const d = await GetDashboardDefaults()
    defaults.provider = d.provider || ''
    defaults.preset = d.preset || ''
    defaults.openCodeProvider = d.openCodeProvider || ''
    defaults.mode = d.mode || 'embedded'
    defaults.shell = d.shell || 'pwsh'
    defaults.claudeMode = d.claudeMode || d.mode || 'embedded'
    defaults.claudeShell = d.claudeShell || d.shell || 'pwsh'
    defaults.openCodeMode = d.openCodeMode || d.mode || 'embedded'
    defaults.openCodeShell = d.openCodeShell || d.shell || 'pwsh'
    defaults.codexMode = d.codexMode || d.mode || 'embedded'
    defaults.codexShell = d.codexShell || d.shell || 'pwsh'
    defaults.useProxy = d.useProxy || false
  } catch (err) {
    console.error('load defaults:', err)
  }
  try {
    shellPaths.value = await GetShellPaths()
  } catch (err) {
    console.error('load shell paths:', err)
  }
  try {
    const t = await GetTerminalSettings()
    terminalScrollback.value = t.scrollback || 100000
  } catch (err) {
    console.error('load terminal settings:', err)
  }
}

const saveDefaults = async () => {
  saving.value = true
  try {
    await SetDashboardDefaults({
      provider: defaults.provider,
      preset: defaults.preset,
      openCodeProvider: defaults.openCodeProvider,
      mode: defaults.claudeMode,
      shell: defaults.claudeShell,
      claudeMode: defaults.claudeMode,
      claudeShell: defaults.claudeShell,
      openCodeMode: defaults.openCodeMode,
      openCodeShell: defaults.openCodeShell,
      codexMode: defaults.codexMode,
      codexShell: defaults.codexShell,
      useProxy: defaults.useProxy,
    } as any)
    showSuccess('默认值已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    saving.value = false
  }
}

const saveTerminal = async () => {
  savingTerminal.value = true
  try {
    const val = Math.max(1000, Math.min(10000000, terminalScrollback.value || 100000))
    await SetTerminalSettings({ scrollback: val } as any)
    terminalScrollback.value = val
    showSuccess('终端设置已保存（重新打开终端后生效）')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    savingTerminal.value = false
  }
}

const addShell = async () => {
  if (!newShellPath.value) return
  try {
    await AddShellPath({ path: newShellPath.value, label: newShellLabel.value || basename(newShellPath.value) } as any)
    shellPaths.value = await GetShellPaths()
    newShellLabel.value = ''
    newShellPath.value = ''
    showSuccess('Shell 路径已添加')
  } catch (err: any) {
    if (err.toString().includes('already exists')) {
      showError('该路径已存在')
    } else {
      showError('添加失败: ' + err)
    }
  }
}

const removeShell = async (path: string) => {
  try {
    await RemoveShellPath(path)
    shellPaths.value = await GetShellPaths()
    showSuccess('已删除')
  } catch (err) {
    showError('删除失败: ' + err)
  }
}

function basename(p: string): string {
  const parts = p.replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || p
}

// --- 远程控制 ---
const remoteStatus = ref<{ host: string; port: number; token: string; running: boolean }>({ host: '0.0.0.0', port: 8680, token: '', running: false })
const remoteEnabled = ref(false)
const remoteToken = ref('')
const remoteHost = ref('0.0.0.0')
const remotePort = ref(8680)
const showToken = ref(false)
const togglingRemote = ref(false)
const savingPort = ref(false)
const regenerating = ref(false)
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const mobileWebRoot = ref('')
const savingWebRoot = ref(false)

async function loadRemoteStatus() {
  try {
    const status = await GetRemoteStatus()
    remoteStatus.value = status as any
    remoteEnabled.value = (status as any).running || false
    remoteToken.value = (status as any).token || ''
    remoteHost.value = (status as any).host || '0.0.0.0'
    remotePort.value = (status as any).port || 8680
    if (remoteEnabled.value && activeTab.value === 'remote') {
      await nextTick()
      await renderQRCode()
    }
  } catch (err) {
    console.error('load remote status:', err)
  }
  try {
    mobileWebRoot.value = await GetMobileWebRoot()
  } catch (err) {
    console.error('load mobile web root:', err)
  }
}

watch(activeTab, async (newTab) => {
  if (newTab === 'remote' && remoteEnabled.value) {
    await nextTick()
    renderQRCode()
  }
})

async function renderQRCode() {
  if (!qrCanvas.value) return
  const localIP = await getLocalIP()
  const url = `http://${localIP}:${remotePort.value}`
  const payload = JSON.stringify({ url, token: remoteToken.value })
  try {
    await QRCode.toCanvas(qrCanvas.value, payload, {
      width: 200,
      margin: 2,
      color: { dark: '#e0e6ed', light: '#1a1f2e' },
    })
  } catch (err) {
    console.error('QR render error:', err)
  }
}

async function getLocalIP(): Promise<string> {
  return new Promise((resolve) => {
    try {
      const pc = new RTCPeerConnection({ iceServers: [] })
      pc.createDataChannel('')
      pc.createOffer().then(offer => pc.setLocalDescription(offer))
      pc.onicecandidate = (e) => {
        if (!e.candidate) return
        const m = e.candidate.candidate.match(/(\d+\.\d+\.\d+\.\d+)/)
        if (m && !m[1].startsWith('127.')) {
          pc.close()
          resolve(m[1])
        }
      }
      setTimeout(() => {
        pc.close()
        resolve('127.0.0.1')
      }, 1500)
    } catch {
      resolve('127.0.0.1')
    }
  })
}

async function toggleRemote() {
  togglingRemote.value = true
  try {
    const newState = !remoteEnabled.value
    await ToggleRemoteServer(newState)
    remoteEnabled.value = newState
    if (newState) {
      showSuccess('远程服务器已启动')
      if (activeTab.value === 'remote') {
        await nextTick()
        await renderQRCode()
      }
    } else {
      showSuccess('远程服务器已停止')
    }
  } catch (err) {
    showError('操作失败: ' + err)
  } finally {
    togglingRemote.value = false
  }
}

async function applyHostPort() {
  savingPort.value = true
  try {
    await SetRemoteHost(remoteHost.value.trim() || '0.0.0.0')
    await SetRemotePort(remotePort.value)
    remoteStatus.value.host = remoteHost.value.trim() || '0.0.0.0'
    remoteStatus.value.port = remotePort.value
    showSuccess('监听地址已更新')
    if (remoteEnabled.value && activeTab.value === 'remote') {
      await nextTick()
      await renderQRCode()
    }
  } catch (err) {
    showError('设置失败: ' + err)
  } finally {
    savingPort.value = false
  }
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(remoteToken.value)
    showSuccess('Token 已复制')
  } catch {
    showError('复制失败')
  }
}

async function regenerateToken() {
  regenerating.value = true
  try {
    const newToken = await RegenerateRemoteToken()
    remoteToken.value = newToken
    showSuccess('Token 已刷新')
    if (remoteEnabled.value && activeTab.value === 'remote') {
      await nextTick()
      await renderQRCode()
    }
  } catch (err) {
    showError('刷新 Token 失败: ' + err)
  } finally {
    regenerating.value = false
  }
}

async function saveMobileWebRoot() {
  savingWebRoot.value = true
  try {
    await SetMobileWebRoot(mobileWebRoot.value.trim())
    showSuccess('移动端 Web 目录已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    savingWebRoot.value = false
  }
}

// --- OpenCode 全局配置 ---
const ocConfigPath = ref('')
const ocEditorContent = ref('')
const ocDiskContent = ref('')    // 上次从磁盘加载/保存的内容，用于 diff
const ocEditorRef = ref<HTMLTextAreaElement | null>(null)
const ocLoading = ref(false)
const ocSaving = ref(false)
const ocReloading = ref(false)
const ocReverting = ref(false)
const ocEditMode = ref<'visual' | 'json'>('visual')
const ocSwitchBlocked = ref(false)

// --- OpenCode Visual GUI State (REAL schema: provider/agent/mcp/permission/instructions/plugin/experimental) ---

interface OcProviderEntry {
  name: string
  apiKey: string
  baseURL: string
  modelsRaw: string
  optionsExtraRaw: string   // unknown fields inside provider.options (store, thinking, etc.)
  extraRaw: string
}

interface OcAgentEntry {
  name: string
  description: string
  mode: 'primary' | 'subagent'
  model: string
  color: string
  prompt: string
  toolsRaw: string
  extraRaw: string
}

interface OcMcpEntry {
  name: string
  type: 'remote' | 'local'
  url: string
  commandRaw: string
  headersRaw: string
  environmentRaw: string
  oauth: boolean
  extraRaw: string
}

interface OcPermEntry {
  key: string
  value: string
}

interface OcKvEntry {
  key: string
  valueRaw: string
}

const ocShowExtraSection = ref(false)

const ocGui = reactive({
  schemaValue: '',
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
  schema: false,
  provider: true,
  agent: false,
  mcp: false,
  permission: false,
  instructions: false,
  plugin: false,
  experimental: false,
  extra: false,
})

// Known top-level keys that have structured sections
const OC_KNOWN_KEYS = new Set([
  '$schema', 'provider', 'agent', 'mcp', 'permission', 'instructions', 'plugin', 'experimental',
])

const ocToggleSection = (section: string) => {
  ocSections[section] = !ocSections[section]
}

// Computed accessor for schema (avoids $ in template)
const ocSchemaValue = computed(() => ocGui.schemaValue)

// Helper: collect unknown keys from an entry object into extraRaw
function collectExtra(entry: Record<string, any>, knownKeys: Set<string>): string {
  const extra: Record<string, any> = {}
  for (const [k, v] of Object.entries(entry)) {
    if (!knownKeys.has(k)) extra[k] = v
  }
  return Object.keys(extra).length > 0 ? JSON.stringify(extra, null, 2) : ''
}

// Helper: parse a raw JSON value string into a JS value (best effort)
function parseJsonValue(raw: string): any {
  const s = raw.trim()
  if (!s) return undefined
  try { return JSON.parse(s) } catch { return s }
}

// Parse raw JSON string into structured GUI state
const ocRawToGui = () => {
  const raw = ocEditorContent.value.trim()
  if (!raw) {
    ocGui.schemaValue = ''
    ocGui.providers = []
    ocGui.agents = []
    ocGui.mcpServers = []
    ocGui.permissions = []
    ocGui.instructions = []
    ocGui.plugins = []
    ocGui.experimentalKvs = []
    ocGui.unknownFieldsRaw = ''
    return
  }
  let obj: any
  try {
    obj = JSON.parse(raw)
  } catch {
    // Invalid JSON -- do NOT touch GUI state to prevent data loss
    return
  }
  if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) return

  // $schema
  ocGui.schemaValue = typeof obj['$schema'] === 'string' ? obj['$schema'] : ''

  // provider: { name: { options?, models?, npm?, name?, ... } }
  const providers: OcProviderEntry[] = []
  if (obj.provider && typeof obj.provider === 'object' && !Array.isArray(obj.provider)) {
    for (const [name, entry] of Object.entries(obj.provider as Record<string, any>)) {
      if (!entry || typeof entry !== 'object') continue
      const PROV_KNOWN = new Set(['options', 'models'])
      const OPTS_KNOWN = new Set(['apiKey', 'baseURL'])
      const opts = entry.options && typeof entry.options === 'object' ? entry.options : {}
      const optionsExtra = collectExtra(opts, OPTS_KNOWN)
      providers.push({
        name,
        apiKey: opts.apiKey || '',
        baseURL: opts.baseURL || '',
        modelsRaw: entry.models && typeof entry.models === 'object' ? JSON.stringify(entry.models, null, 2) : '',
        optionsExtraRaw: optionsExtra,
        extraRaw: collectExtra(entry, PROV_KNOWN),
      })
    }
  }
  ocGui.providers = providers

  // agent: { name: { description?, mode?, model?, color?, prompt?, tools? } }
  const agents: OcAgentEntry[] = []
  if (obj.agent && typeof obj.agent === 'object' && !Array.isArray(obj.agent)) {
    const AGENT_KNOWN = new Set(['description', 'mode', 'model', 'color', 'prompt', 'tools'])
    for (const [name, entry] of Object.entries(obj.agent as Record<string, any>)) {
      if (!entry || typeof entry !== 'object') continue
      agents.push({
        name,
        description: entry.description || '',
        mode: entry.mode === 'primary' ? 'primary' : 'subagent',
        model: entry.model || '',
        color: entry.color || '',
        prompt: entry.prompt || '',
        toolsRaw: entry.tools && typeof entry.tools === 'object' ? JSON.stringify(entry.tools, null, 2) : '',
        extraRaw: collectExtra(entry, AGENT_KNOWN),
      })
    }
  }
  ocGui.agents = agents

  // mcp: { name: { type?, url?, command?, headers?, environment?, oauth? } }
  const mcpServers: OcMcpEntry[] = []
  if (obj.mcp && typeof obj.mcp === 'object' && !Array.isArray(obj.mcp)) {
    const MCP_KNOWN = new Set(['type', 'url', 'command', 'headers', 'environment', 'oauth'])
    for (const [name, entry] of Object.entries(obj.mcp as Record<string, any>)) {
      if (!entry || typeof entry !== 'object') continue
      mcpServers.push({
        name,
        type: entry.type === 'local' ? 'local' : 'remote',
        url: entry.url || '',
        commandRaw: Array.isArray(entry.command) ? JSON.stringify(entry.command) : (entry.command || ''),
        headersRaw: entry.headers && typeof entry.headers === 'object' ? JSON.stringify(entry.headers, null, 2) : '',
        environmentRaw: entry.environment && typeof entry.environment === 'object' ? JSON.stringify(entry.environment, null, 2) : '',
        oauth: !!entry.oauth,
        extraRaw: collectExtra(entry, MCP_KNOWN),
      })
    }
  }
  ocGui.mcpServers = mcpServers

  // permission: { toolName: "allow"|"deny"|"ask" }
  const permissions: OcPermEntry[] = []
  if (obj.permission && typeof obj.permission === 'object' && !Array.isArray(obj.permission)) {
    for (const [key, val] of Object.entries(obj.permission as Record<string, any>)) {
      permissions.push({ key, value: String(val) })
    }
  }
  ocGui.permissions = permissions

  // instructions: string[]
  ocGui.instructions = Array.isArray(obj.instructions)
    ? obj.instructions.filter((s: any) => typeof s === 'string')
    : []

  // plugin: string[]
  ocGui.plugins = Array.isArray(obj.plugin)
    ? obj.plugin.map((p: any) => typeof p === 'string' ? p : JSON.stringify(p))
    : []

  // experimental: { key: value }
  const expKvs: OcKvEntry[] = []
  if (obj.experimental && typeof obj.experimental === 'object' && !Array.isArray(obj.experimental)) {
    for (const [key, val] of Object.entries(obj.experimental as Record<string, any>)) {
      expKvs.push({ key, valueRaw: JSON.stringify(val) })
    }
  }
  ocGui.experimentalKvs = expKvs

  // Unknown fields
  const unknownKeys = Object.keys(obj).filter(k => !OC_KNOWN_KEYS.has(k))
  if (unknownKeys.length > 0) {
    const unknownObj: Record<string, any> = {}
    for (const k of unknownKeys) unknownObj[k] = obj[k]
    ocGui.unknownFieldsRaw = JSON.stringify(unknownObj, null, 2)
    ocShowExtraSection.value = true
  } else {
    ocGui.unknownFieldsRaw = ''
  }
}

// Validate a raw JSON string; returns '' if valid (or empty), error message otherwise
function validateSubJson(raw: string): string {
  const s = raw.trim()
  if (!s) return ''
  try { JSON.parse(s); return '' } catch (e: any) { return e.message || String(e) }
}

// Parse a raw JSON string; returns parsed value or undefined.
// If parse fails, records error into the errors map under the given key.
function parseOrError(raw: string, errors: Record<string, string>, errorKey: string): any {
  const s = raw.trim()
  if (!s) return undefined
  try { return JSON.parse(s) } catch (e: any) {
    errors[errorKey] = e.message || String(e)
    return undefined
  }
}

// Like parseOrError but also requires the result to be a plain JSON object (not array/primitive/null).
// Records a type error if parsed successfully but not an object.
function parseObjectOrError(raw: string, errors: Record<string, string>, errorKey: string): Record<string, any> | undefined {
  const s = raw.trim()
  if (!s) return undefined
  let parsed: any
  try { parsed = JSON.parse(s) } catch (e: any) {
    errors[errorKey] = e.message || String(e)
    return undefined
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    const typeLabel = Array.isArray(parsed) ? 'array' : parsed === null ? 'null' : typeof parsed
    errors[errorKey] = `\u5FC5\u987B\u662F JSON \u5BF9\u8C61 {} \uFF0C\u5F53\u524D\u4E3A ${typeLabel}`
    return undefined
  }
  return parsed
}

// Serialize GUI state back to raw JSON string
// Also populates ocSubJsonErrors with per-field validation results
const ocSubJsonErrors = reactive<Record<string, string>>({})

const ocGuiToRaw = () => {
  // Clear previous sub-JSON errors
  for (const k of Object.keys(ocSubJsonErrors)) delete ocSubJsonErrors[k]

  const result: Record<string, any> = {}

  // $schema
  if (ocGui.schemaValue.trim()) result['$schema'] = ocGui.schemaValue.trim()

  // provider
  if (ocGui.providers.length > 0) {
    const provider: Record<string, any> = {}
    for (const p of ocGui.providers) {
      const name = p.name.trim()
      if (!name) continue
      const entry: Record<string, any> = {}
      // options: merge apiKey + baseURL + optionsExtraRaw
      const options: Record<string, any> = {}
      if (p.apiKey.trim()) options.apiKey = p.apiKey.trim()
      if (p.baseURL.trim()) options.baseURL = p.baseURL.trim()
      // Parse optionsExtraRaw and merge into options
      const optionsExtra = parseObjectOrError(p.optionsExtraRaw, ocSubJsonErrors, `provider.${name}.optionsExtra`)
      if (optionsExtra !== undefined) {
        Object.assign(options, optionsExtra)
      }
      if (Object.keys(options).length > 0) entry.options = options
      // models
      const models = parseOrError(p.modelsRaw, ocSubJsonErrors, `provider.${name}.models`)
      if (models !== undefined) entry.models = models
      // extra (entry-level unknowns like npm, name)
      const provExtra = parseObjectOrError(p.extraRaw, ocSubJsonErrors, `provider.${name}.extra`)
      if (provExtra !== undefined) Object.assign(entry, provExtra)
      provider[name] = entry
    }
    if (Object.keys(provider).length > 0) result.provider = provider
  }

  // agent
  if (ocGui.agents.length > 0) {
    const agent: Record<string, any> = {}
    for (const a of ocGui.agents) {
      const name = a.name.trim()
      if (!name) continue
      const entry: Record<string, any> = {}
      if (a.description.trim()) entry.description = a.description.trim()
      if (a.mode) entry.mode = a.mode
      if (a.model.trim()) entry.model = a.model.trim()
      if (a.color.trim()) entry.color = a.color.trim()
      if (a.prompt.trim()) entry.prompt = a.prompt.trim()
      const tools = parseOrError(a.toolsRaw, ocSubJsonErrors, `agent.${name}.tools`)
      if (tools !== undefined) entry.tools = tools
      const agentExtra = parseObjectOrError(a.extraRaw, ocSubJsonErrors, `agent.${name}.extra`)
      if (agentExtra !== undefined) Object.assign(entry, agentExtra)
      agent[name] = entry
    }
    if (Object.keys(agent).length > 0) result.agent = agent
  }

  // mcp
  if (ocGui.mcpServers.length > 0) {
    const mcp: Record<string, any> = {}
    for (const m of ocGui.mcpServers) {
      const name = m.name.trim()
      if (!name) continue
      const entry: Record<string, any> = { type: m.type }
      if (m.type === 'remote' && m.url.trim()) entry.url = m.url.trim()
      if (m.type === 'local' && m.commandRaw.trim()) {
        const cmd = parseOrError(m.commandRaw, ocSubJsonErrors, `mcp.${name}.command`)
        if (cmd !== undefined) {
          entry.command = Array.isArray(cmd) ? cmd : m.commandRaw.trim().split(/\s+/)
        }
      }
      const headers = parseOrError(m.headersRaw, ocSubJsonErrors, `mcp.${name}.headers`)
      if (headers !== undefined) entry.headers = headers
      const env = parseOrError(m.environmentRaw, ocSubJsonErrors, `mcp.${name}.environment`)
      if (env !== undefined) entry.environment = env
      if (m.oauth) entry.oauth = true
      const mcpExtra = parseObjectOrError(m.extraRaw, ocSubJsonErrors, `mcp.${name}.extra`)
      if (mcpExtra !== undefined) Object.assign(entry, mcpExtra)
      mcp[name] = entry
    }
    if (Object.keys(mcp).length > 0) result.mcp = mcp
  }

  // permission
  if (ocGui.permissions.length > 0) {
    const permission: Record<string, string> = {}
    for (const p of ocGui.permissions) {
      if (p.key.trim()) permission[p.key.trim()] = p.value
    }
    if (Object.keys(permission).length > 0) result.permission = permission
  }

  // instructions
  const instrs = ocGui.instructions.filter(s => s.trim())
  if (instrs.length > 0) result.instructions = instrs

  // plugin
  const plugins = ocGui.plugins.filter(s => s.trim())
  if (plugins.length > 0) result.plugin = plugins

  // experimental
  if (ocGui.experimentalKvs.length > 0) {
    const experimental: Record<string, any> = {}
    for (const kv of ocGui.experimentalKvs) {
      if (kv.key.trim()) {
        const parsed = parseJsonValue(kv.valueRaw)
        if (parsed !== undefined) experimental[kv.key.trim()] = parsed
      }
    }
    if (Object.keys(experimental).length > 0) result.experimental = experimental
  }

  // Unknown fields
  const unknowns = parseObjectOrError(ocGui.unknownFieldsRaw, ocSubJsonErrors, 'unknownFields')
  if (unknowns !== undefined) {
    Object.assign(result, unknowns)
  }

  ocEditorContent.value = Object.keys(result).length > 0
    ? JSON.stringify(result, null, 2) + '\n'
    : '{\n}\n'
}

// Section add/remove helpers
const ocAddProvider = () => {
  ocGui.providers.push({ name: '', apiKey: '', baseURL: '', modelsRaw: '', optionsExtraRaw: '', extraRaw: '' })
  if (!ocSections.provider) ocSections.provider = true
}
const ocRemoveProvider = (idx: number) => { ocGui.providers.splice(idx, 1); ocGuiToRaw() }

const ocAddAgent = () => {
  ocGui.agents.push({ name: '', description: '', mode: 'subagent', model: '', color: '', prompt: '', toolsRaw: '', extraRaw: '' })
  if (!ocSections.agent) ocSections.agent = true
}
const ocRemoveAgent = (idx: number) => { ocGui.agents.splice(idx, 1); ocGuiToRaw() }

const ocAddMcp = () => {
  ocGui.mcpServers.push({ name: '', type: 'remote', url: '', commandRaw: '', headersRaw: '', environmentRaw: '', oauth: false, extraRaw: '' })
  if (!ocSections.mcp) ocSections.mcp = true
}
const ocRemoveMcp = (idx: number) => { ocGui.mcpServers.splice(idx, 1); ocGuiToRaw() }

const ocAddPermission = () => {
  ocGui.permissions.push({ key: '', value: 'allow' })
  if (!ocSections.permission) ocSections.permission = true
}
const ocRemovePermission = (idx: number) => { ocGui.permissions.splice(idx, 1); ocGuiToRaw() }

const ocAddInstruction = () => {
  ocGui.instructions.push('')
  if (!ocSections.instructions) ocSections.instructions = true
}
const ocRemoveInstruction = (idx: number) => { ocGui.instructions.splice(idx, 1); ocGuiToRaw() }

const ocAddPlugin = () => {
  ocGui.plugins.push('')
  if (!ocSections.plugin) ocSections.plugin = true
}
const ocRemovePlugin = (idx: number) => { ocGui.plugins.splice(idx, 1); ocGuiToRaw() }

const ocAddExperimental = () => {
  ocGui.experimentalKvs.push({ key: '', valueRaw: 'true' })
  if (!ocSections.experimental) ocSections.experimental = true
}
const ocRemoveExperimental = (idx: number) => { ocGui.experimentalKvs.splice(idx, 1); ocGuiToRaw() }

// Mode switching -- SAFE: block visual switch when JSON is invalid
const ocSwitchToVisual = () => {
  // If JSON is invalid, block the switch
  if (ocValidationError.value !== '' && ocValidationError.value !== null) {
    ocSwitchBlocked.value = true
    return
  }
  ocSwitchBlocked.value = false
  ocRawToGui()
  ocEditMode.value = 'visual'
}
const ocSwitchToJson = () => {
  // Sync visual to JSON first, then check for sub-JSON errors
  if (ocEditMode.value === 'visual') {
    ocGuiToRaw()
  }
  // Block switch if sub-JSON fields are invalid
  if (ocHasSubJsonErrors.value) {
    ocSwitchBlocked.value = true
    return
  }
  ocSwitchBlocked.value = false
  ocEditMode.value = 'json'
}

// Three-tier validation result:
//   null  -> empty / nothing to validate
//   ''    -> valid JSON object (safely saveable)
//   string -> specific error message
const ocValidationError = computed<string | null>(() => {
  const text = ocEditorContent.value.trim()
  if (!text) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch (e: any) {
    const msg = e.message || String(e)
    // Chrome/V8 produces messages like:
    //   "Unexpected token } in JSON at position 5"
    //   "Expected property name or '}' in JSON at position 2"
    // Keep the most useful part
    const posMatch = msg.match(/(at position \d+|at line \d+ column \d+)/i)
    if (posMatch) {
      const before = msg.substring(0, posMatch.index).trim()
      return before + ' ' + posMatch[0]
    }
    // Truncate very long messages
    return msg.length > 140 ? msg.substring(0, 140) + '...' : msg
  }
  // Root must be an object
  if (parsed === null) return '根节点不能为 null，必须为 JSON 对象 {}'
  if (Array.isArray(parsed)) return '根节点不能为数组，必须为 JSON 对象 {}'
  if (typeof parsed !== 'object') {
    const type = typeof parsed
    return `根节点不能为 ${type === 'string' ? '字符串' : type === 'number' ? '数字' : type === 'boolean' ? '布尔值' : type}，必须为 JSON 对象 {}`
  }
  return ''
})

const ocIsParseableJson = computed(() => {
  const text = ocEditorContent.value.trim()
  if (!text) return false
  try { JSON.parse(text); return true } catch { return false }
})

const ocIsRootObject = computed(() => {
  return ocValidationError.value === ''
})

const ocHasSubJsonErrors = computed(() => Object.keys(ocSubJsonErrors).length > 0)

const ocCanSave = computed(() => ocIsRootObject.value && !ocHasSubJsonErrors.value)

const ocValidationClass = computed(() => {
  if (ocValidationError.value === null) return 'neutral'
  if (ocValidationError.value === '') return 'valid'
  return 'invalid'
})

const ocValidationText = computed(() => {
  if (ocValidationError.value === null) return '空'
  if (ocValidationError.value === '') return 'JSON 合法'
  return 'JSON 非法'
})

const ocHasUnsavedChanges = computed(() => {
  return ocEditorContent.value !== ocDiskContent.value
})

const ocHasSensitiveHint = computed(() => {
  const text = ocEditorContent.value.toLowerCase()
  return text.includes('key') || text.includes('token') || text.includes('secret') || text.includes('password')
})

async function ocLoad() {
  ocLoading.value = true
  try {
    const [content, path] = await Promise.all([
      GetOpenCodeConfig(),
      GetOpenCodeConfigPath(),
    ])
    ocEditorContent.value = content
    ocDiskContent.value = content
    ocConfigPath.value = path
    // Populate GUI state from loaded JSON
    ocRawToGui()
  } catch (err) {
    showError('加载 OpenCode 配置失败: ' + err)
  } finally {
    ocLoading.value = false
  }
}

async function ocReload() {
  ocReloading.value = true
  try {
    const content = await GetOpenCodeConfig()
    ocEditorContent.value = content
    ocDiskContent.value = content
    ocRawToGui()
    showSuccess('已重新加载配置')
  } catch (err) {
    showError('重新加载失败: ' + err)
  } finally {
    ocReloading.value = false
  }
}

async function ocSave() {
  if (!ocCanSave.value) return
  // Sync visual state to JSON before saving
  if (ocEditMode.value === 'visual') {
    ocGuiToRaw()
  }
  ocSaving.value = true
  try {
    await SaveOpenCodeConfig(ocEditorContent.value)
    // Reload from disk to get the canonical formatted version
    const content = await GetOpenCodeConfig()
    ocEditorContent.value = content
    ocDiskContent.value = content
    ocRawToGui()
    showSuccess('OpenCode 配置已保存')
  } catch (err) {
    showError('保存失败: ' + err)
  } finally {
    ocSaving.value = false
  }
}

function ocFormat() {
  if (!ocIsParseableJson.value) return
  try {
    const parsed = JSON.parse(ocEditorContent.value)
    ocEditorContent.value = JSON.stringify(parsed, null, 2) + '\n'
  } catch {
    // Should not happen since ocIsParseableJson is true
  }
}

async function ocRevert() {
  ocReverting.value = true
  try {
    const content = await GetOpenCodeConfig()
    ocEditorContent.value = content
    ocDiskContent.value = content
    ocRawToGui()
    showSuccess('已恢复到磁盘内容')
  } catch (err) {
    showError('恢复失败: ' + err)
  } finally {
    ocReverting.value = false
  }
}

async function copyConfigPath() {
  try {
    await navigator.clipboard.writeText(ocConfigPath.value)
    showSuccess('路径已复制')
  } catch {
    showError('复制失败')
  }
}

function handleTabKey(e: KeyboardEvent) {
  const el = ocEditorRef.value
  if (!el) return
  const start = el.selectionStart
  const end = el.selectionEnd
  const val = ocEditorContent.value
  ocEditorContent.value = val.substring(0, start) + '  ' + val.substring(end)
  nextTick(() => {
    el.selectionStart = el.selectionEnd = start + 2
  })
}

// Watch tab to load OpenCode config on first visit
watch(activeTab, (newTab) => {
  if (newTab === 'opencode' && !ocConfigPath.value) {
    ocLoad()
  }
})

onMounted(async () => {
  await loadData()
  await loadRemoteStatus()
  try {
    const info = await GetAppInfo()
    currentVersion.value = (info as any).version || ''
  } catch {}
  try {
    githubToken.value = await GetGitHubToken()
  } catch {}
})
</script>

<style scoped>
/* App Colors */
.settings-layout {
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
.settings-sidebar {
  width: 200px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 24px;
}

.nav-tabs {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.nav-tab {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  background: transparent;
  border: none;
  border-left: 3px solid transparent;
  border-radius: 0 6px 6px 0;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 14px;
  font-family: inherit;
  transition: background 0.2s, border-color 0.2s, color 0.2s;
  text-align: left;
}

.nav-tab:hover {
  background: var(--surface);
  color: var(--text-primary);
}

.nav-tab.active {
  border-left-color: var(--accent);
  background: rgba(79, 195, 247, 0.08);
  color: var(--accent);
  font-weight: 500;
}

.tab-icon {
  font-size: 16px;
  width: 20px;
  text-align: center;
}

/* Content Area */
.settings-content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding-right: 16px;
  position: relative;
}

/* Transitions */
.fade-slide-enter-active,
.fade-slide-leave-active {
  transition: all 0.2s cubic-bezier(0.25, 0.8, 0.25, 1);
}
.fade-slide-enter-from {
  opacity: 0;
  transform: translateX(15px);
}
.fade-slide-leave-to {
  opacity: 0;
  transform: translateX(-15px);
}

.settings-section {
  padding-bottom: 40px;
}

.section-header {
  margin-bottom: 32px;
}

.section-header h2 {
  font-size: 20px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 8px 0;
}

.section-header p {
  color: var(--text-secondary);
  font-size: 14px;
  margin: 0;
}

.group-header {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-secondary);
  margin: 0 0 16px 0;
  font-weight: 600;
}

.group-separator {
  height: 1px;
  background: var(--border);
  margin: 32px 0;
}

.setting-group {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-row {
  display: flex;
  gap: 24px;
}

.flex-1 { flex: 1; }

.form-group label {
  display: block;
  margin-bottom: 8px;
  color: var(--text-secondary);
  font-size: 14px;
}

.field-desc {
  color: var(--text-muted);
  font-size: 12px;
}

/* Inputs */
.input-field {
  width: 100%;
  background: var(--bg);
  border: 1px solid var(--border);
  color: var(--text-primary);
  padding: 10px 12px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  transition: border-color 0.15s, box-shadow 0.15s;
  outline: none;
  box-sizing: border-box;
}

.input-field:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px rgba(79, 195, 247, 0.15);
}

.input-field:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.monospace {
  font-family: monospace;
}

/* Select */
.select-wrapper {
  position: relative;
}

.select-wrapper::after {
  content: '▼';
  font-size: 10px;
  color: var(--text-muted);
  position: absolute;
  right: 12px;
  top: 50%;
  transform: translateY(-50%);
  pointer-events: none;
}

.select-wrapper .input-field {
  appearance: none;
  -webkit-appearance: none;
  padding-right: 32px;
}

/* Inline Inputs */
.inline-input-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.token-input {
  letter-spacing: 2px;
}

/* Buttons */
.btn {
  padding: 10px 20px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: transform 0.15s, box-shadow 0.15s, background 0.15s;
  border: none;
  outline: none;
  background: var(--surface);
  color: var(--text-primary);
  border: 1px solid var(--border);
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none !important;
  box-shadow: none !important;
}

.btn.small {
  padding: 6px 14px;
  font-size: 13px;
}

.btn.primary {
  background: var(--accent);
  color: var(--bg);
  border-color: transparent;
}

.btn.primary:hover:not(:disabled) {
  background: var(--accent-hover);
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(79, 195, 247, 0.2);
}

.btn.danger {
  background: transparent;
  color: var(--error);
  border-color: var(--error);
}

.btn.danger:hover:not(:disabled) {
  background: rgba(239, 83, 80, 0.1);
}

/* Toggle Switches */
.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px;
  background: var(--surface);
  border-radius: 8px;
}

.toggle-info label {
  color: var(--text-primary);
  font-size: 14px;
  margin: 0;
}

.toggle-info .field-desc {
  margin-top: 4px;
  display: block;
}

.ios-toggle {
  position: relative;
  width: 44px;
  height: 24px;
  background: var(--border);
  border-radius: 24px;
  cursor: pointer;
  transition: background 0.2s ease;
  border: none;
  outline: none;
  flex-shrink: 0;
}

.ios-toggle.large-toggle {
  width: 52px;
  height: 28px;
  border-radius: 28px;
}

.ios-toggle.active {
  background: var(--accent);
}

.ios-toggle::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 20px;
  height: 20px;
  background: #fff;
  border-radius: 50%;
  transition: transform 0.2s cubic-bezier(0.25, 0.8, 0.25, 1), background 0.2s;
  box-shadow: 0 2px 4px rgba(0,0,0,0.2);
}

.ios-toggle.large-toggle::after {
  width: 24px;
  height: 24px;
}

.ios-toggle.active::after {
  transform: translateX(20px);
}

.ios-toggle.large-toggle.active::after {
  transform: translateX(24px);
}

/* Engine Tabs */
.engine-tabs {
  display: inline-flex;
  background: var(--surface);
  border-radius: 8px;
  padding: 4px;
  gap: 4px;
  border: 1px solid var(--border);
}

.engine-tab {
  flex: 1;
  padding: 8px 16px;
  background: transparent;
  border: none;
  border-radius: 6px;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  transition: all 0.2s;
}

.engine-tab:hover {
  color: var(--text-primary);
}

.engine-tab.active {
  background: var(--elevated);
  color: var(--text-primary);
  box-shadow: 0 1px 3px rgba(0,0,0,0.2);
}

/* Mode Selector */
.mode-selector {
  display: flex;
  gap: 12px;
}

.mode-btn {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-secondary);
  cursor: pointer;
  transition: all 0.2s ease;
}

.mode-btn:hover {
  border-color: var(--border-hover);
  background: var(--elevated);
  transform: translateY(-1px);
}

.mode-btn.active {
  border-color: var(--accent);
  color: var(--accent);
  background: rgba(79, 195, 247, 0.05);
  box-shadow: 0 0 0 1px var(--accent) inset, 0 4px 12px rgba(79, 195, 247, 0.1);
}

.mode-icon { font-size: 20px; }
.mode-label { font-size: 14px; font-weight: 500; }

/* Shell Pills */
.shell-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.shell-pill {
  padding: 8px 16px;
  background: var(--surface);
  border: 1px solid transparent;
  border-radius: 20px;
  color: var(--text-secondary);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.shell-pill:hover {
  background: var(--elevated);
  color: var(--text-primary);
}

.shell-pill.active {
  background: var(--accent);
  color: var(--bg);
  font-weight: 600;
}

/* Shell Paths */
.add-shell-card {
  display: flex;
  gap: 12px;
  background: var(--surface);
  padding: 16px;
  border-radius: 8px;
  border: 1px solid var(--border);
}

.shell-list {
  display: flex;
  flex-direction: column;
}

.shell-list-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  transition: background 0.2s, border-radius 0.2s;
}

.shell-list-item:hover {
  background: var(--surface);
  border-radius: 6px;
  border-bottom-color: transparent;
}

.shell-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.shell-label {
  font-size: 14px;
  color: var(--text-primary);
  font-weight: 500;
}

.shell-path {
  font-size: 12px;
  color: var(--text-muted);
}

.delete-btn {
  opacity: 0;
  transition: opacity 0.2s;
}

.shell-list-item:hover .delete-btn {
  opacity: 1;
}

.empty-state {
  padding: 32px;
  text-align: center;
  background: var(--surface);
  border: 1px dashed var(--border);
  border-radius: 8px;
  color: var(--text-muted);
  font-size: 14px;
}

/* Slider */
.range-with-input {
  display: flex;
  align-items: center;
  gap: 20px;
}

.range-slider {
  appearance: none;
  background: var(--surface);
  height: 6px;
  border-radius: 3px;
  outline: none;
}

.range-slider::-webkit-slider-thumb {
  appearance: none;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--accent);
  cursor: pointer;
  transition: transform 0.1s;
}

.range-slider::-webkit-slider-thumb:hover {
  transform: scale(1.2);
}

/* Remote */
.remote-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24px;
  background: var(--surface);
  border-radius: 12px;
  border: 1px solid var(--border);
}

.remote-status {
  display: flex;
  align-items: center;
  gap: 16px;
}

.status-ring {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--text-muted);
  position: relative;
}

.remote-status.active .status-ring {
  background: var(--success);
  box-shadow: 0 0 0 4px rgba(102, 187, 106, 0.2);
}

.status-info h4 {
  margin: 0 0 4px 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text-primary);
}

.status-info p {
  margin: 0;
  font-size: 13px;
  color: var(--text-secondary);
}

.qr-frame {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  background: var(--surface);
  border-radius: 12px;
  padding: 24px;
  border: 1px solid var(--border);
  max-width: max-content;
}

.qr-canvas {
  border-radius: 8px;
  overflow: hidden;
}

.qr-frame p {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}

/* Updates */
.update-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--surface);
  padding: 16px 24px;
  border-radius: 8px;
  border: 1px solid var(--border);
}

.version-info {
  display: flex;
  align-items: center;
  gap: 16px;
}

.version-label {
  color: var(--text-secondary);
  font-size: 14px;
}

.version-badge {
  display: inline-block;
  padding: 4px 12px;
  background: rgba(79, 195, 247, 0.1);
  color: var(--accent);
  border-radius: 20px;
  font-family: monospace;
  font-weight: 600;
}

.update-card {
  background: var(--surface);
  border-left: 4px solid var(--success);
  border-radius: 8px;
  padding: 24px;
  margin-top: 24px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
}

.update-card-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.status-dot.online { background: var(--success); }

.update-title {
  color: var(--text-primary);
  font-weight: 600;
  font-size: 16px;
}

.update-version-new {
  color: var(--success);
  font-family: monospace;
  font-weight: 600;
}

.update-date {
  color: var(--text-muted);
  font-size: 12px;
  margin: 8px 0 16px 0;
}

.release-notes {
  background: var(--bg);
  padding: 16px;
  border-radius: 6px;
  margin-bottom: 24px;
  max-height: 200px;
  overflow-y: auto;
}

.release-notes pre {
  margin: 0;
  color: var(--text-secondary);
  font-size: 13px;
  font-family: inherit;
  white-space: pre-wrap;
  line-height: 1.5;
}

.progress-container {
  margin-top: 16px;
  display: flex;
  align-items: center;
  gap: 16px;
}

.progress-bar {
  flex: 1;
  height: 6px;
  background: var(--border);
  border-radius: 3px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 3px;
  transition: width 0.3s ease;
}

.progress-text {
  color: var(--text-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.update-uptodate {
  margin-top: 24px;
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--success);
  font-size: 14px;
}

.update-error {
  margin-top: 16px;
  color: var(--error);
  font-size: 13px;
}

/* About */
.about-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 0;
}

.app-logo {
  width: 80px;
  height: 80px;
  background: var(--surface);
  border-radius: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 24px;
  box-shadow: 0 8px 24px rgba(0,0,0,0.2);
  border: 1px solid var(--border);
}

.app-icon {
  font-size: 36px;
  color: var(--accent);
}

.app-name {
  font-size: 28px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0 0 8px 0;
}

.app-version {
  color: var(--text-muted);
  font-size: 14px;
  margin: 0 0 40px 0;
}

.about-details {
  width: 100%;
  max-width: 400px;
  background: var(--surface);
  border-radius: 8px;
  padding: 16px 24px;
  border: 1px solid var(--border);
}

.detail-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.detail-label { color: var(--text-secondary); font-size: 14px; }
.detail-value { color: var(--text-primary); font-size: 14px; }

.section-footer {
  margin-top: 32px;
  display: flex;
  justify-content: flex-end;
}

/* OpenCode Config */
.opencode-path {
  font-size: 13px;
  color: var(--text-muted);
}

.opencode-notice {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
  padding: 12px 16px;
  background: rgba(255, 183, 77, 0.08);
  border: 1px solid rgba(255, 183, 77, 0.25);
  border-radius: 8px;
  color: #ffb74d;
  font-size: 13px;
  line-height: 1.5;
}

.notice-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: rgba(255, 183, 77, 0.2);
  font-size: 12px;
  font-weight: 700;
  flex-shrink: 0;
  font-style: normal;
}

.opencode-editor-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.opencode-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}

.opencode-unsaved-badge {
  display: inline-block;
  padding: 3px 10px;
  background: rgba(79, 195, 247, 0.1);
  color: var(--accent);
  border-radius: 12px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.3px;
}

.oc-validation {
  font-size: 12px;
  font-weight: 500;
}

.oc-validation.neutral {
  color: var(--text-muted);
}

.oc-validation.valid {
  color: var(--success);
}

.oc-validation.invalid {
  color: var(--error);
}

.oc-error-detail {
  margin-top: 8px;
  padding: 8px 12px;
  background: rgba(239, 83, 80, 0.08);
  border: 1px solid rgba(239, 83, 80, 0.2);
  border-radius: 6px;
  color: #ef9a9a;
  font-size: 12px;
  font-family: 'Cascadia Code', 'Fira Code', 'JetBrains Mono', 'Consolas', monospace;
  line-height: 1.5;
  word-break: break-all;
}

.opencode-editor-wrap {
  position: relative;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid var(--border);
  transition: border-color 0.15s;
}

.opencode-editor-wrap:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px rgba(79, 195, 247, 0.15);
}

.opencode-editor {
  width: 100%;
  min-height: 380px;
  max-height: 60vh;
  resize: vertical;
  padding: 16px;
  background: var(--bg);
  color: var(--text-primary);
  border: none;
  outline: none;
  font-family: 'Cascadia Code', 'Fira Code', 'JetBrains Mono', 'Consolas', monospace;
  font-size: 13px;
  line-height: 1.6;
  tab-size: 2;
  white-space: pre;
  overflow: auto;
}

.opencode-editor::placeholder {
  color: var(--text-muted);
}

.opencode-editor::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

.opencode-editor::-webkit-scrollbar-track {
  background: transparent;
}

.opencode-editor::-webkit-scrollbar-thumb {
  background: var(--border);
  border-radius: 4px;
}

.opencode-editor::-webkit-scrollbar-thumb:hover {
  background: var(--border-hover);
}

.opencode-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 16px;
}

.opencode-actions-spacer {
  flex: 1;
}

/* OpenCode Visual Mode */
.oc-header-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
}

.oc-mode-switch {
  display: inline-flex;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 3px;
  gap: 2px;
  flex-shrink: 0;
}

.oc-mode-btn {
  padding: 6px 16px;
  background: transparent;
  border: none;
  border-radius: 6px;
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.15s ease;
}

.oc-mode-btn:hover {
  color: var(--text-primary);
}

.oc-mode-btn.active {
  background: var(--accent);
  color: var(--bg);
  font-weight: 600;
}

.oc-status-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
  min-height: 24px;
}

.oc-switch-warning {
  font-size: 12px;
  color: var(--error);
  font-weight: 500;
}

.oc-sub-error {
  display: block;
  font-size: 11px;
  color: var(--error);
  margin-top: 2px;
  margin-bottom: 4px;
  padding: 2px 6px;
  background: color-mix(in srgb, var(--error) 10%, transparent);
  border-radius: 3px;
}

.oc-visual-mode {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.oc-section {
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  transition: border-color 0.15s;
}

.oc-section:hover {
  border-color: var(--border-hover);
}

.oc-section-header {
  padding: 12px 18px;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 500;
  color: var(--text-secondary);
  transition: background-color 0.12s ease, color 0.12s ease;
  user-select: none;
  background: var(--surface);
}

.oc-section-header:hover {
  background: rgba(79, 195, 247, 0.04);
  color: var(--text-primary);
}

.oc-collapse-icon {
  font-size: 10px;
  color: var(--accent);
  width: 14px;
  text-align: center;
}

.oc-count-badge {
  font-size: 11px;
  font-weight: 600;
  background: rgba(79, 195, 247, 0.15);
  color: var(--accent);
  padding: 1px 7px;
  border-radius: 10px;
  margin-left: 4px;
}

.oc-section-body {
  padding: 12px 18px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.oc-card {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.oc-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.oc-card-name {
  font-weight: 600;
  font-size: 13px;
  color: var(--accent);
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
}

.oc-remove-btn {
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 4px 6px;
  border-radius: 4px;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1;
  transition: all 0.15s ease;
}

.oc-remove-btn:hover {
  background: rgba(239, 83, 80, 0.1);
  color: var(--error);
}

.oc-kv-row {
  display: flex;
  gap: 8px;
  align-items: center;
}

.oc-kv-row .input-field {
  flex: 1;
}

.oc-mini-textarea {
  min-height: 48px;
  resize: vertical;
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  font-size: 13px;
  line-height: 1.5;
  tab-size: 2;
}
</style>
