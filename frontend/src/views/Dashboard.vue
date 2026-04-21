<template>
  <div class="dashboard">
    <!-- Header with Indicators + Action Buttons -->
    <div class="dashboard-header">
      <div class="header-left">
        <h1 class="page-title">仪表盘</h1>
        <div class="header-indicators">
          <span class="indicator">
            <span class="indicator-dot" :class="{ active: runningCount > 0 }"></span>
            {{ runningCount }} 终端运行中
          </span>
          <span class="indicator" v-if="proxyStatus">
            <span class="indicator-dot" :class="{ active: proxyStatus?.running }"></span>
            代理 {{ proxyStatus?.running ? '运行中' : '未启动' }}
          </span>
        </div>
      </div>

    </div>

    <!-- Sessions (moved UP, right below header) -->
    <div class="card sessions-card" v-if="sessions.length > 0">
      <div class="card-header sessions-header">
        <h2>终端会话</h2>
        <div class="sessions-actions">
          <button class="btn small" @click="handleClearStopped" v-if="hasStoppedSessions">
            清除已结束
          </button>
        </div>
      </div>

      <div class="sessions-content">
        <div class="session-tabs-container">
          <div class="session-tabs">
            <div
              v-for="sess in sessions"
              :key="sess.id"
              class="session-tab"
              :class="[`status-${sess.status}`, { selected: selectedSession === sess.id }]"
              @click="selectedSession = selectedSession === sess.id ? '' : sess.id"
            >
              <div class="session-tab-header">
                <span class="session-mode-icon">
                  <svg v-if="sess.mode === 'embedded'" viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line></svg>
                  <svg v-else viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2" ry="2"></rect><polyline points="9 9 15 12 9 15"></polyline></svg>
                </span>
                <span class="session-id">#{{ sess.id }}</span>
                <span class="session-status-dot" :class="`dot-${sess.status}`"></span>
              </div>
              <div class="session-tab-info">
                <span class="session-provider">{{ sess.provider || 'Local' }}</span>
                <span class="session-model">{{ sess.model || sess.preset || 'Default' }}</span>
              </div>
            </div>
          </div>
        </div>

        <div class="session-detail" v-if="selectedSessionData">
          <div class="detail-grid">
            <div class="detail-item">
              <span class="detail-label">状态</span>
              <span class="detail-value" :class="`text-${selectedSessionData.status}`">
                {{ statusLabel(selectedSessionData.status) }}
              </span>
            </div>
            <div class="detail-item">
              <span class="detail-label">提供商</span>
              <span class="detail-value">{{ selectedSessionData.provider || '-' }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">预设</span>
              <span class="detail-value">{{ selectedSessionData.preset || '-' }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">模型</span>
              <span class="detail-value">{{ selectedSessionData.model || '-' }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">模式</span>
              <span class="detail-value">{{ getModeLabel(selectedSessionData.mode) }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">工作目录</span>
              <span class="detail-value path-value" :title="selectedSessionData.workDir">{{ selectedSessionData.workDir }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">PID</span>
              <span class="detail-value">{{ selectedSessionData.pid || '-' }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">启动时间</span>
              <span class="detail-value">{{ selectedSessionData.startedAt }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">运行时长</span>
              <span class="detail-value">{{ selectedSessionData.duration || '-' }}</span>
            </div>
          </div>

          <div class="detail-actions">
            <button
              class="btn danger small"
              v-if="selectedSessionData.status === 'running'"
              @click="handleStopSession(selectedSessionData.id)"
            >
              停止运行
            </button>
            <button
              class="btn secondary small"
              v-if="selectedSessionData.status !== 'running'"
              @click="handleRemoveSession(selectedSessionData.id)"
            >
              移除记录
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Quick Launch -->
    <div class="card launch-card">
      <div class="launch-tabs">
        <button
          class="launch-tab"
          :class="{ active: activeLaunchTab === 'claudecode' }"
          @click="activeLaunchTab = 'claudecode'"
        >
          ClaudeCode
        </button>
        <button
          class="launch-tab"
          :class="{ active: activeLaunchTab === 'opencode' }"
          @click="activeLaunchTab = 'opencode'"
        >
          OpenCode
        </button>
        <button
          class="launch-tab"
          :class="{ active: activeLaunchTab === 'codex' }"
          @click="activeLaunchTab = 'codex'"
        >
          Codex
        </button>
        <button
          class="launch-tab"
          :class="{ active: activeLaunchTab === 'amagicode' }"
          @click="activeLaunchTab = 'amagicode'"
        >
          AmagiCode
        </button>
      </div>

      <div class="launch-content">
        <div class="launch-content-header">
          <button
            class="btn primary launch-action-btn"
            @click="handleLaunchByTab()"
            :disabled="!canLaunch || loading"
          >
            <svg viewBox="0 0 24 24" width="15" height="15" stroke="currentColor" stroke-width="2.5" fill="none" stroke-linecap="round" stroke-linejoin="round" style="margin-right: 6px;"><polygon points="5 3 19 12 5 21 5 3"></polygon></svg>
            {{ loading ? '处理中...' : '启动终端' }}
          </button>
          <button class="btn danger launch-action-btn" v-if="runningCount > 0" @click="handleStopAll" :disabled="loading">
            <svg viewBox="0 0 24 24" width="13" height="13" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" style="margin-right: 5px;"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"></rect></svg>
            停止全部
          </button>
        </div>

        <div class="workspace-status-card" v-if="hasWorkspaceStatus">
          <div class="workspace-status-copy">
            <span class="workspace-status-label">工作区状态</span>
            <strong class="workspace-status-title" v-if="matchedWorkspace">当前目录已登记为工作区：{{ matchedWorkspace.name || basename(matchedWorkspace.path) }}</strong>
            <strong class="workspace-status-title" v-else>当前目录尚未登记为工作区</strong>
            <span class="workspace-status-path">{{ selectedWorkDir }}</span>
          </div>
          <button class="btn secondary small" @click="goToWorkspaceManager">{{ matchedWorkspace ? '管理工作区' : '登记工作区' }}</button>
        </div>

        <!-- ClaudeCode -->
        <div v-if="activeLaunchTab === 'claudecode'" class="launch-tab-content">
          <div class="form-row">
            <div class="form-group flex-1">
              <label>服务提供商</label>
              <select v-model="selectedProvider" class="input-field">
                <option v-for="(provider, name) in anthropicProviders" :key="name" :value="name">
                  {{ name }}
                </option>
              </select>
            </div>
            <div class="form-group flex-1">
              <label>预设配置</label>
              <select v-model="selectedPreset" class="input-field" :disabled="!hasPresets">
                <option v-for="(preset, name) in availablePresets" :key="name" :value="name">
                  {{ name }} ({{ preset.model }})
                </option>
              </select>
            </div>
          </div>

          <div class="form-row">
            <div class="form-group flex-1">
              <label>启动模式</label>
              <div class="mode-selector">
                <button
                  v-for="m in launchModes"
                  :key="m.value"
                  class="mode-btn"
                  :class="{ active: claudeMode === m.value }"
                  @click="claudeMode = m.value"
                >
                  <span class="mode-icon">
                    <svg v-if="m.value === 'embedded'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line></svg>
                    <svg v-else viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2" ry="2"></rect><polyline points="9 9 15 12 9 15"></polyline></svg>
                  </span>
                  <span class="mode-label">{{ m.label }}</span>
                </button>
              </div>
            </div>

            <div class="form-group flex-1" v-if="claudeMode === 'embedded'">
              <label>终端 Shell 路径</label>
              <div class="shell-selector">
                <div class="shell-tabs">
                  <button
                    v-for="s in shellOptions"
                    :key="s.value"
                    class="shell-tab"
                    :class="{ active: claudeShell === s.value }"
                    @click="claudeShell = s.value"
                    :title="s.value || '直接启动 claude（不经过 shell）'"
                  >
                    {{ s.label }}
                  </button>
                </div>
                <div class="shell-input-row" v-if="claudeShell === '__custom__'">
                  <input
                    type="text"
                    class="input-field"
                    v-model="claudeCustomShellPath"
                    placeholder="输入 shell 可执行文件路径"
                  />
                </div>
              </div>
            </div>
          </div>

          <div class="form-group">
            <label>工作目录</label>
            <div class="path-selector">
              <div class="path-tabs" v-if="savedPaths.length > 0">
                <button
                  v-for="p in savedPaths"
                  :key="p.path"
                  class="path-tab"
                  :class="{ active: selectedWorkDir === p.path }"
                  @click="selectedWorkDir = p.path"
                  :title="p.path"
                >
                  {{ p.label || basename(p.path) }}
                  <span class="path-tab-remove" @click.stop="removeSavedPath(p.path)">
                    <svg viewBox="0 0 24 24" width="12" height="12" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                  </span>
                </button>
              </div>
              <div class="path-input-row">
                <input
                  type="text"
                  class="input-field"
                  v-model="selectedWorkDir"
                  placeholder="输入或选择工作目录..."
                />
                <button class="btn icon-btn" @click="browseDirectory" title="浏览目录">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>
                </button>
                <button
                  class="btn icon-btn"
                  @click="saveCurrentPath"
                  :disabled="!selectedWorkDir"
                  title="保存当前路径"
                >
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"></path><polyline points="17 21 17 13 7 13 7 21"></polyline><polyline points="7 3 7 8 15 8"></polyline></svg>
                </button>
              </div>
            </div>
          </div>

          <div class="form-group checkbox-group">
            <label class="checkbox-label">
              <input type="checkbox" v-model="useProxy" />
              <span class="checkbox-text">启用注入代理</span>
            </label>
          </div>
        </div>

        <!-- OpenCode -->
        <div v-if="activeLaunchTab === 'opencode'" class="launch-tab-content">
          <div class="form-row">
            <div class="form-group flex-1">
              <label>服务提供商</label>
              <select v-model="selectedOpenCodeProvider" class="input-field">
                <option value="">不指定（沿用本机 OpenCode 登录）</option>
                <option v-for="(provider, name) in openCodeProviders" :key="name" :value="name">
                  {{ name }}
                </option>
              </select>
            </div>
            <div class="form-group flex-1">
              <label>预设配置</label>
              <select v-model="selectedOpenCodePreset" class="input-field" :disabled="!selectedOpenCodeProvider || !hasOpenCodePresets">
                <option value="">不指定（默认配置）</option>
                <option v-for="(preset, name) in openCodeAvailablePresets" :key="name" :value="name">
                  {{ name }}{{ preset.model ? ` (${preset.model})` : '' }}
                </option>
              </select>
            </div>
          </div>

          <div class="form-row">
            <div class="form-group flex-1">
              <label>启动模式</label>
              <div class="mode-selector">
                <button
                  v-for="m in launchModes"
                  :key="m.value"
                  class="mode-btn"
                  :class="{ active: openCodeMode === m.value }"
                  @click="openCodeMode = m.value"
                >
                  <span class="mode-icon">
                    <svg v-if="m.value === 'embedded'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line></svg>
                    <svg v-else viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2" ry="2"></rect><polyline points="9 9 15 12 9 15"></polyline></svg>
                  </span>
                  <span class="mode-label">{{ m.label }}</span>
                </button>
              </div>
            </div>

            <div class="form-group flex-1" v-if="openCodeMode === 'embedded'">
              <label>终端 Shell 路径</label>
              <div class="shell-selector">
                <div class="shell-tabs">
                  <button
                    v-for="s in shellOptions"
                    :key="s.value"
                    class="shell-tab"
                    :class="{ active: openCodeShell === s.value }"
                    @click="openCodeShell = s.value"
                    :title="s.value || '直接启动（不经过 shell）'"
                  >
                    {{ s.label }}
                  </button>
                </div>
                <div class="shell-input-row" v-if="openCodeShell === '__custom__'">
                  <input
                    type="text"
                    class="input-field"
                    v-model="openCodeCustomShellPath"
                    placeholder="输入 shell 可执行文件路径"
                  />
                </div>
              </div>
            </div>
          </div>

          <div class="form-group">
            <label>工作目录</label>
            <div class="path-selector">
              <div class="path-tabs" v-if="savedPaths.length > 0">
                <button
                  v-for="p in savedPaths"
                  :key="p.path"
                  class="path-tab"
                  :class="{ active: selectedWorkDir === p.path }"
                  @click="selectedWorkDir = p.path"
                  :title="p.path"
                >
                  {{ p.label || basename(p.path) }}
                  <span class="path-tab-remove" @click.stop="removeSavedPath(p.path)">
                    <svg viewBox="0 0 24 24" width="12" height="12" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                  </span>
                </button>
              </div>
              <div class="path-input-row">
                <input
                  type="text"
                  class="input-field"
                  v-model="selectedWorkDir"
                  placeholder="输入或选择工作目录..."
                />
                <button class="btn icon-btn" @click="browseDirectory" title="浏览目录">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>
                </button>
                <button
                  class="btn icon-btn"
                  @click="saveCurrentPath"
                  :disabled="!selectedWorkDir"
                  title="保存当前路径"
                >
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"></path><polyline points="17 21 17 13 7 13 7 21"></polyline><polyline points="7 3 7 8 15 8"></polyline></svg>
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Codex -->
        <div v-if="activeLaunchTab === 'codex'" class="launch-tab-content">
          <div class="form-row">
            <div class="form-group flex-1">
              <label>服务提供商</label>
              <select v-model="selectedCodexProvider" class="input-field">
                <option v-for="(provider, name) in openaiProviders" :key="name" :value="name">
                  {{ name }}
                </option>
              </select>
            </div>
            <div class="form-group flex-1">
              <label>模型</label>
              <select v-model="selectedCodexModel" class="input-field" :disabled="!selectedCodexProvider">
                <option v-for="model in codexAvailableModels" :key="model" :value="model">
                  {{ model }}
                </option>
              </select>
            </div>
          </div>

          <div class="form-row">
            <div class="form-group flex-1">
              <label>启动模式</label>
              <div class="mode-selector">
                <button
                  v-for="m in codexLaunchModes"
                  :key="m.value"
                  class="mode-btn"
                  :class="{ active: codexMode === m.value }"
                  @click="codexMode = m.value"
                >
                  <span class="mode-icon">
                    <svg v-if="m.value === 'embedded'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line></svg>
                    <svg v-else viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2" ry="2"></rect><polyline points="9 9 15 12 9 15"></polyline></svg>
                  </span>
                  <span class="mode-label">{{ m.label }}</span>
                </button>
              </div>
            </div>

            <div class="form-group flex-1" v-if="codexMode === 'embedded'">
              <label>终端 Shell 路径</label>
              <div class="shell-selector">
                <div class="shell-tabs">
                  <button
                    v-for="s in shellOptions"
                    :key="s.value"
                    class="shell-tab"
                    :class="{ active: codexShell === s.value }"
                    @click="codexShell = s.value"
                    :title="s.value || '直接启动（不经过 shell）'"
                  >
                    {{ s.label }}
                  </button>
                </div>
                <div class="shell-input-row" v-if="codexShell === '__custom__'">
                  <input
                    type="text"
                    class="input-field"
                    v-model="codexCustomShellPath"
                    placeholder="输入 shell 可执行文件路径"
                  />
                </div>
              </div>
            </div>
          </div>

          <div class="form-group">
            <label>工作目录</label>
            <div class="path-selector">
              <div class="path-tabs" v-if="savedPaths.length > 0">
                <button
                  v-for="p in savedPaths"
                  :key="p.path"
                  class="path-tab"
                  :class="{ active: selectedWorkDir === p.path }"
                  @click="selectedWorkDir = p.path"
                  :title="p.path"
                >
                  {{ p.label || basename(p.path) }}
                  <span class="path-tab-remove" @click.stop="removeSavedPath(p.path)">
                    <svg viewBox="0 0 24 24" width="12" height="12" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                  </span>
                </button>
              </div>
              <div class="path-input-row">
                <input
                  type="text"
                  class="input-field"
                  v-model="selectedWorkDir"
                  placeholder="输入或选择工作目录..."
                />
                <button class="btn icon-btn" @click="browseDirectory" title="浏览目录">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>
                </button>
                <button
                  class="btn icon-btn"
                  @click="saveCurrentPath"
                  :disabled="!selectedWorkDir"
                  title="保存当前路径"
                >
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"></path><polyline points="17 21 17 13 7 13 7 21"></polyline><polyline points="7 3 7 8 15 8"></polyline></svg>
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- AmagiCode -->
        <div v-if="activeLaunchTab === 'amagicode'" class="launch-tab-content">
          <div class="form-row">
            <div class="form-group flex-1">
              <label>预设配置</label>
              <select v-model="amagiCodePreset" class="input-field" :disabled="!hasAmagiPresets">
                <option value="">请选择 ModelPreset...</option>
                <option v-for="(group, name) in amagiAvailablePresets" :key="name" :value="name">
                  {{ name }} ({{ getGroupSummary(group) }})
                </option>
              </select>
            </div>
          </div>

          <div class="form-row">
            <div class="form-group flex-1">
              <label>启动模式</label>
              <div class="mode-selector">
                <button
                  v-for="m in launchModes"
                  :key="m.value"
                  class="mode-btn"
                  :class="{ active: amagiCodeMode === m.value }"
                  @click="amagiCodeMode = m.value"
                >
                  <span class="mode-icon">
                    <svg v-if="m.value === 'embedded'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line></svg>
                    <svg v-else viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2" ry="2"></rect><polyline points="9 9 15 12 9 15"></polyline></svg>
                  </span>
                  <span class="mode-label">{{ m.label }}</span>
                </button>
              </div>
            </div>

            <div class="form-group flex-1" v-if="amagiCodeMode === 'embedded'">
              <label>终端 Shell 路径</label>
              <div class="shell-selector">
                <div class="shell-tabs">
                  <button
                    v-for="s in shellOptions"
                    :key="s.value"
                    class="shell-tab"
                    :class="{ active: amagiCodeShell === s.value }"
                    @click="amagiCodeShell = s.value"
                    :title="s.value || '直接启动 AmagiCode（不经过 shell）'"
                  >
                    {{ s.label }}
                  </button>
                </div>
                <div class="shell-input-row" v-if="amagiCodeShell === '__custom__'">
                  <input
                    type="text"
                    class="input-field"
                    v-model="amagiCodeCustomShellPath"
                    placeholder="输入 shell 可执行文件路径"
                  />
                </div>
              </div>
            </div>
          </div>

          <div class="form-group">
            <label>工作目录</label>
            <div class="path-selector">
              <div class="path-tabs" v-if="savedPaths.length > 0">
                <button
                  v-for="p in savedPaths"
                  :key="p.path"
                  class="path-tab"
                  :class="{ active: selectedWorkDir === p.path }"
                  @click="selectedWorkDir = p.path"
                  :title="p.path"
                >
                  {{ p.label || basename(p.path) }}
                  <span class="path-tab-remove" @click.stop="removeSavedPath(p.path)">
                    <svg viewBox="0 0 24 24" width="12" height="12" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                  </span>
                </button>
              </div>
              <div class="path-input-row">
                <input
                  type="text"
                  class="input-field"
                  v-model="selectedWorkDir"
                  placeholder="输入或选择工作目录..."
                />
                <button class="btn icon-btn" @click="browseDirectory" title="浏览目录">
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>
                </button>
                <button
                  class="btn icon-btn"
                  @click="saveCurrentPath"
                  :disabled="!selectedWorkDir"
                  title="保存当前路径"
                >
                  <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"></path><polyline points="17 21 17 13 7 13 7 21"></polyline><polyline points="7 3 7 8 15 8"></polyline></svg>
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { ref, computed, onMounted, onUnmounted, watch, toRef } from 'vue'
import { useRouter } from 'vue-router'
import { LaunchSession, StopSession, StopAllSessions, GetSessions, RemoveSession, ClearStoppedSessions, BrowseDirectory, GetAmagiSettings, LaunchAmagiCode } from '../../wailsjs/go/main/App'

// Codex API -- bindings will be auto-generated after wails build;
// use window.go fallback for now to avoid import errors before regeneration
const LaunchCodexSession = (modelName: string, providerID: string, mode: string, workDir: string, shellPath: string): Promise<string> =>
  (window as any)['go']['main']['App']['LaunchCodexSession'](modelName, providerID, mode, workDir, shellPath)

// OpenCode uses wails binding with 5 args: (providerName, presetName, mode, workDir, shellPath)
const LaunchOpenCodeWithProvider = (providerName: string, presetName: string, mode: string, workDir: string, shellPath: string): Promise<string> =>
  (window as any)['go']['main']['App']['LaunchOpenCode'](providerName, presetName, mode, workDir, shellPath)

import { GetProviders } from '../../wailsjs/go/config/ConfigService'
import { GetStatus as GetProxyStatus } from '../../wailsjs/go/proxy/ProxyService'
import { GetPaths, AddPath, RemovePath, GetDefaultPath } from '../../wailsjs/go/paths/PathsService'
import { GetDashboardDefaults, GetShellPaths, SetDashboardDefaults } from '../../wailsjs/go/settings/Service'
import { ListWorkspaces } from '../../wailsjs/go/workspace/Service'
import { config, proxy, workspace } from '../../wailsjs/go/models'
import { useToast } from '../composables/useToast'
import { useDashboardState } from '../composables/useDashboardState'

const router = useRouter()
const dashState = useDashboardState()

const providers = ref<Record<string, config.Provider>>({})
const proxyStatus = ref<proxy.ProxyStatus | null>(null)
const sessions = ref<any[]>([])
const workspaces = ref<workspace.Workspace[]>([])

// 使用共享状态（跨路由保持）
const selectedProvider = toRef(dashState, 'provider')
const selectedPreset = toRef(dashState, 'preset')
const selectedOpenCodeProvider = toRef(dashState, 'openCodeProvider')
const selectedOpenCodePreset = toRef(dashState, 'openCodePreset')
const claudeMode = toRef(dashState, 'claudeMode')
const openCodeMode = toRef(dashState, 'openCodeMode')
const codexMode = toRef(dashState, 'codexMode')
const selectedWorkDir = toRef(dashState, 'workDir')
const useProxy = toRef(dashState, 'useProxy')
const claudeShell = toRef(dashState, 'claudeShell')
const openCodeShell = toRef(dashState, 'openCodeShell')
const codexShell = toRef(dashState, 'codexShell')
const claudeCustomShellPath = toRef(dashState, 'claudeCustomShellPath')
const openCodeCustomShellPath = toRef(dashState, 'openCodeCustomShellPath')
const codexCustomShellPath = toRef(dashState, 'codexCustomShellPath')
const amagiCodePreset = toRef(dashState, 'amagiCodePreset')
const amagiCodeMode = toRef(dashState, 'amagiCodeMode')
const amagiCodeShell = toRef(dashState, 'amagiCodeShell')
const amagiCodeCustomShellPath = toRef(dashState, 'amagiCodeCustomShellPath')

const loading = ref(false)
const selectedSession = ref('')
const savedPaths = ref<Array<{ path: string; label: string }>>([])
const savedShellPaths = ref<Array<{ path: string; label: string }>>([])

// Codex 状态
const selectedCodexProvider = ref('')
const selectedCodexModel = ref('')

// ClaudeCode: filter providers excluding type="openai" (legacy providers without type are treated as anthropic)
const anthropicProviders = computed(() => {
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (!provider.type || provider.type !== 'openai') {
      result[name] = provider
    }
  }
  return result
})

// Codex: filter providers with type="openai"
const openaiProviders = computed(() => {
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (provider.type === 'openai' || provider.auth_key === 'OPENAI_API_KEY') {
      result[name] = provider
    }
  }
  return result
})

const openCodeProviders = computed(() => {
  // OpenCode only works with openai-type providers (same filter as Codex).
  // This matches the backend behavior: openai-compatible providers derive
  // OpenCode provider IDs correctly, while anthropic-type providers are
  // designed for Claude Code / AmagiCode, not OpenCode.
  const result: Record<string, config.Provider> = {}
  for (const [name, provider] of Object.entries(providers.value)) {
    if (provider.type === 'openai' || provider.auth_key === 'OPENAI_API_KEY') {
      result[name] = provider
    }
  }
  return result
})

// OpenCode presets: filter to only show presets with target=opencode
const openCodeAvailablePresets = computed(() => {
  const prov = openCodeProviders.value[selectedOpenCodeProvider.value]
  if (!prov || !prov.presets) return {}
  const result: Record<string, config.Preset> = {}
  for (const [name, preset] of Object.entries(prov.presets)) {
    if (preset.target === 'opencode') {
      result[name] = preset
    }
  }
  return result
})

// Codex presets: filter to only show presets with target=codex or empty target
const codexAvailablePresetsFiltered = computed(() => {
  if (!selectedCodexProvider.value || !openaiProviders.value[selectedCodexProvider.value]) return {}
  const presets = openaiProviders.value[selectedCodexProvider.value].presets || {}
  const result: Record<string, config.Preset> = {}
  for (const [name, preset] of Object.entries(presets)) {
    if (!preset.target || preset.target === 'codex') {
      result[name] = preset
    }
  }
  return result
})

// ClaudeCode presets: filter to only show presets with target=codex or empty target
const claudeCodeAvailablePresets = computed(() => {
  if (!selectedProvider.value || !providers.value[selectedProvider.value]) return {}
  const presets = providers.value[selectedProvider.value].presets || {}
  const result: Record<string, config.Preset> = {}
  for (const [name, preset] of Object.entries(presets)) {
    if (!preset.target || preset.target === 'codex') {
      result[name] = preset
    }
  }
  return result
})

const codexAvailablePresets = computed(() => {
  return codexAvailablePresetsFiltered.value
})

const codexAvailableModels = computed(() => {
  const presets = codexAvailablePresets.value
  return Object.values(presets).map(p => p.model).filter(Boolean)
})

// AmagiCode 预设组（从 settings_amagi.json 的 modelPresets，现在是 ModelPresetGroup）
const amagiAvailablePresets = ref<Record<string, any>>({})

function getGroupSummary(group: any): string {
  const presets = group?.presets || {}
  const count = Object.keys(presets).length
  const defaultPreset = group?.default_preset || ''
  if (count === 0) return '空组'
  let summary = `${count} 个预设`
  if (defaultPreset) summary += `, 默认: ${defaultPreset}`
  return summary
}

const hasAmagiPresets = computed(() => Object.keys(amagiAvailablePresets.value).length > 0)

// AmagiCode shell 路径解析函数
function resolveAmagiCodeShellPath(): string {
  switch (amagiCodeShell.value) {
    case '': return ''
    case 'pwsh': return 'C:\\Program Files\\PowerShell\\7\\pwsh.exe'
    case 'powershell': return 'powershell.exe'
    case 'cmd': return 'cmd.exe'
    case '__custom__': return amagiCodeCustomShellPath.value
    default: return amagiCodeShell.value
  }
}

// Codex 仅支持内嵌终端和独立窗口（无 VSCode/Zed 集成）
const codexLaunchModes = [
  { value: 'embedded', label: '内嵌终端', icon: '\u25A8' },
  { value: 'terminal', label: '独立窗口', icon: '\u2B1B' },
]

// 启动类型 Tabs
const activeLaunchTab = ref<'claudecode' | 'opencode' | 'codex' | 'amagicode'>('claudecode')

// Shell 路径选项（固定 + 自定义）
const builtinShellOptions = [
  { value: '', label: '直接 Claude' },
  { value: 'pwsh', label: 'PowerShell 7' },
  { value: 'powershell', label: 'Windows PowerShell' },
  { value: 'cmd', label: 'CMD' },
]

const shellOptions = computed(() => [
  ...builtinShellOptions,
  ...savedShellPaths.value.map(s => ({ value: s.path, label: s.label })),
  { value: '__custom__', label: '自定义路径' },
])

// 获取实际 shell 路径
function resolveShellPath(): string {
  switch (claudeShell.value) {
    case '': return ''
    case 'pwsh': return 'C:\\Program Files\\PowerShell\\7\\pwsh.exe'
    case 'powershell': return 'powershell.exe'
    case 'cmd': return 'cmd.exe'
    case '__custom__': return claudeCustomShellPath.value
    default: return claudeShell.value
  }
}

function resolveOpenCodeShellPath(): string {
  switch (openCodeShell.value) {
    case '': return ''
    case 'pwsh': return 'C:\\Program Files\\PowerShell\\7\\pwsh.exe'
    case 'powershell': return 'powershell.exe'
    case 'cmd': return 'cmd.exe'
    case '__custom__': return openCodeCustomShellPath.value
    default: return openCodeShell.value
  }
}

function resolveCodexShellPath(): string {
  switch (codexShell.value) {
    case '': return ''
    case 'pwsh': return 'C:\\Program Files\\PowerShell\\7\\pwsh.exe'
    case 'powershell': return 'powershell.exe'
    case 'cmd': return 'cmd.exe'
    case '__custom__': return codexCustomShellPath.value
    default: return codexShell.value
  }
}

const { showSuccess, showError } = useToast()

let refreshInterval: number | null = null

const launchModes = [
  { value: 'embedded', label: '内嵌终端', icon: '▨' },
  { value: 'terminal', label: '独立窗口', icon: '⬛' },
]

const availablePresets = computed(() => {
  return claudeCodeAvailablePresets.value
})

const hasPresets = computed(() => Object.keys(availablePresets.value).length > 0)

const hasOpenCodePresets = computed(() => Object.keys(openCodeAvailablePresets.value).length > 0)

const canLaunch = computed(() => {
  if (activeLaunchTab.value === 'claudecode') {
    return selectedProvider.value && selectedPreset.value
  } else if (activeLaunchTab.value === 'codex') {
    // provider 和 model 均为可选
    return true
  } else if (activeLaunchTab.value === 'amagicode') {
    // AmagiCode 只需要预设，provider 从预设中获取
    return !!amagiCodePreset.value
  } else {
    // OpenCode 只需要工作目录，provider 可选
    return !!selectedWorkDir.value
  }
})

const runningCount = computed(() => sessions.value.filter(s => s.status === 'running').length)

const hasStoppedSessions = computed(() => sessions.value.some(s => s.status !== 'running'))

const selectedSessionData = computed(() => {
  if (!selectedSession.value) return null
  return sessions.value.find(s => s.id === selectedSession.value) || null
})

const normalizeWorkspacePath = (value: string) => value.split('\\').join('/').replace(/\/+$/, '').trim().toLowerCase()
const matchedWorkspace = computed(() => {
  if (!selectedWorkDir.value) return null
  const currentPath = normalizeWorkspacePath(selectedWorkDir.value)
  return workspaces.value.find(item => normalizeWorkspacePath(item.path) === currentPath) || null
})
const hasWorkspaceStatus = computed(() => Boolean(selectedWorkDir.value))


watch(selectedProvider, (newVal) => {
  if (newVal && providers.value[newVal]) {
    const presets = claudeCodeAvailablePresets.value
    const presetKeys = Object.keys(presets)
    if (presetKeys.length > 0) {
      if (!presetKeys.includes(selectedPreset.value)) {
        selectedPreset.value = presetKeys[0]
      }
    } else {
      selectedPreset.value = ''
    }
  } else {
    selectedPreset.value = ''
  }
})

watch(selectedCodexProvider, () => {
  const models = codexAvailableModels.value
  if (models.length > 0) {
    if (!models.includes(selectedCodexModel.value)) {
      selectedCodexModel.value = models[0]
    }
  } else {
    selectedCodexModel.value = ''
  }
})

watch(selectedOpenCodeProvider, () => {
  const presets = openCodeAvailablePresets.value
  const keys = Object.keys(presets)
  if (keys.length > 0) {
    if (!keys.includes(selectedOpenCodePreset.value)) {
      selectedOpenCodePreset.value = keys[0]
    }
  } else {
    selectedOpenCodePreset.value = ''
  }
})


const loadShellPaths = async () => {
  try {
    savedShellPaths.value = await GetShellPaths()
  } catch (err) {
    console.error('Failed to load shell paths:', err)
  }
}

const loadProviders = async () => {
  try {
    providers.value = await GetProviders()
  } catch (err) {
    console.error('Failed to load providers:', err)
    showError('加载提供商失败: ' + err)
  }
}

const loadAmagiSettings = async () => {
  try {
    const settings = await GetAmagiSettings()
    if (settings && settings.model_presets) {
      amagiAvailablePresets.value = settings.model_presets
      // 如果有激活组且当前未选择，自动选择
      if (settings.model && !amagiCodePreset.value) {
        amagiCodePreset.value = settings.model
      }
    }
  } catch (err) {
    console.error('Failed to load AmagiCode settings:', err)
  }
}

const initDefaults = async () => {
  if (dashState.initialized) return
  try {
    const d = await GetDashboardDefaults()
    if (d.provider) dashState.provider = d.provider
    if (d.preset) dashState.preset = d.preset
    dashState.openCodeProvider = d.openCodeProvider || ''
    dashState.openCodePreset = d.openCodePreset || ''
    dashState.claudeMode = d.claudeMode || d.mode || 'embedded'
    dashState.openCodeMode = d.openCodeMode || d.mode || 'embedded'
    dashState.codexMode = d.codexMode || d.mode || 'embedded'
    // TODO: Add amagiCodeMode/amagiCodeShell/amagiCodeProvider/amagiCodePreset to backend DashboardDefaults
    dashState.amagiCodeMode = d.amagiCodeMode || d.mode || 'embedded'
    dashState.claudeShell = d.claudeShell || d.shell || 'pwsh'
    dashState.openCodeShell = d.openCodeShell || d.shell || 'pwsh'
    dashState.codexShell = d.codexShell || d.shell || 'pwsh'
    dashState.amagiCodeShell = d.amagiCodeShell || d.shell || 'pwsh'
    dashState.amagiCodePreset = d.amagiCodePreset || ''
    dashState.useProxy = d.useProxy || false
  } catch (err) {
    console.error('Failed to load defaults:', err)
  }
  // 若没有设置默认 provider，选第一个
  if (!selectedProvider.value && Object.keys(anthropicProviders.value).length > 0) {
    selectedProvider.value = Object.keys(anthropicProviders.value)[0]
  }
  dashState.initialized = true
}

const persistDashboardDefaults = async () => {
  try {
    await SetDashboardDefaults({
      provider: selectedProvider.value,
      preset: selectedPreset.value,
      openCodeProvider: selectedOpenCodeProvider.value,
      openCodePreset: selectedOpenCodePreset.value,
      mode: claudeMode.value,
      shell: claudeShell.value,
      claudeMode: claudeMode.value,
      claudeShell: claudeShell.value,
      openCodeMode: openCodeMode.value,
      openCodeShell: openCodeShell.value,
      codexMode: codexMode.value,
      codexShell: codexShell.value,
      amagiCodeMode: amagiCodeMode.value,
      amagiCodeShell: amagiCodeShell.value,
      amagiCodePreset: amagiCodePreset.value,
      useProxy: useProxy.value,
    } as any)
  } catch (err) {
    console.error('Failed to persist dashboard defaults:', err)
  }
}

const loadPaths = async () => {
  try {
    savedPaths.value = await GetPaths()
    const defaultPath = await GetDefaultPath()
    if (defaultPath && !selectedWorkDir.value) {
      selectedWorkDir.value = defaultPath
    }
  } catch (err) {
    console.error('Failed to load paths:', err)
  }
}

const loadWorkspaces = async () => {
  try {
    workspaces.value = await ListWorkspaces()
  } catch (err) {
    console.error('Failed to load workspaces:', err)
  }
}

const refreshStatus = async () => {
  try {
    proxyStatus.value = await GetProxyStatus()
    sessions.value = await GetSessions()
  } catch (err) {
    console.error('Failed to refresh status:', err)
  }
}

const goToWorkspaceManager = () => {
  if (!selectedWorkDir.value) return
  if (matchedWorkspace.value) {
    router.push({ path: '/extensions/workspaces', query: { workspaceId: matchedWorkspace.value.id } })
    return
  }
  router.push({ path: '/extensions/workspaces', query: { path: selectedWorkDir.value } })
}

const handleLaunch = async () => {
  if (!canLaunch.value || activeLaunchTab.value !== 'claudecode') return
  loading.value = true
  try {
    const sessionId = await LaunchSession(
      selectedProvider.value,
      selectedPreset.value,
      claudeMode.value,
      selectedWorkDir.value,
      useProxy.value,
      claudeMode.value === 'embedded' ? resolveShellPath() : '',
    )
    await persistDashboardDefaults()
    await refreshStatus()
    selectedSession.value = sessionId
    showSuccess('终端启动成功')
    // 内嵌模式自动跳转到终端页面
    if (claudeMode.value === 'embedded') {
      router.push('/terminals')
    }
  } catch (err) {
    console.error('Launch failed:', err)
    showError('启动失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleLaunchOpenCode = async () => {
  if (!canLaunch.value || activeLaunchTab.value !== 'opencode') return
  loading.value = true
  try {
    const shellPath = openCodeMode.value === 'embedded' ? resolveOpenCodeShellPath() : ''
    const sessionId = await LaunchOpenCodeWithProvider(
      selectedOpenCodeProvider.value,
      selectedOpenCodePreset.value,
      openCodeMode.value,
      selectedWorkDir.value,
      shellPath,
    )
    await persistDashboardDefaults()
    await refreshStatus()
    selectedSession.value = sessionId
    showSuccess('OpenCode 启动成功')
    // 内嵌模式自动跳转到终端页面
    if (openCodeMode.value === 'embedded') {
      router.push('/terminals')
    }
  } catch (err) {
    console.error('Launch OpenCode failed:', err)
    showError('启动 OpenCode 失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleLaunchCodex = async () => {
  if (!canLaunch.value || activeLaunchTab.value !== 'codex') return
  loading.value = true
  try {
    const shellPath = codexMode.value === 'embedded' ? resolveCodexShellPath() : ''
    const sessionId = await LaunchCodexSession(
      selectedCodexModel.value,
      selectedCodexProvider.value,
      codexMode.value,
      selectedWorkDir.value,
      shellPath,
    )
    await persistDashboardDefaults()
    await refreshStatus()
    selectedSession.value = sessionId
    showSuccess('Codex 启动成功')
    if (codexMode.value === 'embedded') {
      router.push('/terminals')
    }
  } catch (err) {
    console.error('Launch Codex failed:', err)
    showError('启动 Codex 失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleLaunchAmagiCode = async () => {
  if (!canLaunch.value || activeLaunchTab.value !== 'amagicode') return
  loading.value = true
  try {
    const shellPath = amagiCodeMode.value === 'embedded' ? resolveAmagiCodeShellPath() : ''
    const sessionId = await LaunchAmagiCode(
      amagiCodePreset.value,
      '',
      amagiCodeMode.value,
      selectedWorkDir.value,
      shellPath,
    )
    await persistDashboardDefaults()
    await refreshStatus()
    selectedSession.value = sessionId
    showSuccess('AmagiCode 启动成功')
    if (amagiCodeMode.value === 'embedded') {
      router.push('/terminals')
    }
  } catch (err) {
    console.error('Launch AmagiCode failed:', err)
    showError('启动 AmagiCode 失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleLaunchByTab = () => {
  if (activeLaunchTab.value === 'claudecode') {
    handleLaunch()
  } else if (activeLaunchTab.value === 'codex') {
    handleLaunchCodex()
  } else if (activeLaunchTab.value === 'amagicode') {
    handleLaunchAmagiCode()
  } else {
    handleLaunchOpenCode()
  }
}

const handleStopSession = async (id: string) => {
  try {
    await StopSession(id)
    await refreshStatus()
    showSuccess('终端已停止')
  } catch (err) {
    showError('停止失败: ' + err)
  }
}

const handleStopAll = async () => {
  loading.value = true
  try {
    await StopAllSessions()
    await refreshStatus()
    showSuccess('全部终端已停止')
  } catch (err) {
    showError('停止失败: ' + err)
  } finally {
    loading.value = false
  }
}

const handleRemoveSession = async (id: string) => {
  try {
    await RemoveSession(id)
    if (selectedSession.value === id) selectedSession.value = ''
    await refreshStatus()
  } catch (err) {
    showError('移除失败: ' + err)
  }
}

const handleClearStopped = async () => {
  try {
    const count = await ClearStoppedSessions()
    selectedSession.value = ''
    await refreshStatus()
    showSuccess(`已清除 ${count} 个已结束会话`)
  } catch (err) {
    showError('清除失败: ' + err)
  }
}

const browseDirectory = async () => {
  try {
    const dir = await BrowseDirectory()
    if (dir) selectedWorkDir.value = dir
  } catch (err) {
    showError('选择目录失败: ' + err)
  }
}

const saveCurrentPath = async () => {
  if (!selectedWorkDir.value) return
  try {
    await AddPath({ path: selectedWorkDir.value, label: basename(selectedWorkDir.value) })
    await loadPaths()
    showSuccess('路径已保存')
  } catch (err: any) {
    if (err.toString().includes('already exists')) {
      showError('该路径已保存')
    } else {
      showError('保存路径失败: ' + err)
    }
  }
}

const removeSavedPath = async (path: string) => {
  try {
    await RemovePath(path)
    await loadPaths()
  } catch (err) {
    showError('删除路径失败: ' + err)
  }
}

function basename(p: string): string {
  const parts = p.replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || p
}

function getModeIcon(mode: string): string {
  const m = launchModes.find(lm => lm.value === mode)
  return m?.icon || '⬛'
}

function getModeLabel(mode: string): string {
  const m = launchModes.find(lm => lm.value === mode)
  return m?.label || mode
}

function statusLabel(status: string): string {
  const map: Record<string, string> = {
    running: '运行中',
    stopped: '已停止',
    exited: '已退出',
    failed: '启动失败',
  }
  return map[status] || status
}

onMounted(async () => {
  await loadProviders()
  await loadAmagiSettings()
  await initDefaults()
  await loadPaths()
  await loadShellPaths()
  await loadWorkspaces()
  await refreshStatus()
  refreshInterval = window.setInterval(refreshStatus, 2000)
})

onUnmounted(() => {
  if (refreshInterval) clearInterval(refreshInterval)
})
</script>
<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 24px;
  max-width: 1200px;
  margin: 0 auto;
  padding-bottom: 40px;

  min-height: 100%;
}

/* Header & Indicators */
.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  margin-bottom: 4px;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: #e0e6ed;
}

.header-indicators {
  display: flex;
  gap: 16px;
  align-items: center;
}

.indicator {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: #8899aa;
  background: rgba(15, 18, 25, 0.5);
  padding: 6px 12px;
  border-radius: 20px;
  border: 1px solid #2a2f3e;
}

.indicator-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #5a6a7a;
  transition: all 0.3s ease;
}

.indicator-dot.active {
  background: #66bb6a;
  box-shadow: 0 0 8px rgba(102, 187, 106, 0.4);
}

/* Card Base */
.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  overflow: hidden;
}

/* Launch Card */
.launch-card {
  display: flex;
  flex-direction: column;

  flex: 1;
}

.launch-tabs {
  display: flex;
  gap: 2px;
  background: #0f1219;
  border-bottom: 1px solid #2a2f3e;
  padding: 0 12px;
}

.launch-tab {
  padding: 14px 24px;
  background: transparent;
  border: none;
  color: #8899aa;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  position: relative;
  font-family: inherit;
}

.launch-tab:hover {
  color: #ccd6e0;
}

.launch-tab.active {
  color: #4fc3f7;
}

.launch-tab.active::after {
  content: '';
  position: absolute;
  bottom: -1px;
  left: 0;
  right: 0;
  height: 2px;
  background: #4fc3f7;
}

.launch-content {
  padding: 24px;
}

.launch-content-header {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 12px;
  padding-bottom: 20px;
  margin-bottom: 20px;
  border-bottom: 1px solid rgba(42, 47, 62, 0.5);
}

.launch-action-btn {
  padding: 10px 28px;
  font-size: 14px;
}

.launch-tab-content {
  display: flex;
  flex-direction: column;
  gap: 20px;
  animation: fadeIn 0.2s ease;
}

.workspace-status-card {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  margin-bottom: 20px;
  padding: 14px 16px;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  background: rgba(15, 18, 25, 0.45);
}

.workspace-status-copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.workspace-status-label {
  font-size: 12px;
  font-weight: 600;
  color: #8899aa;
}

.workspace-status-title {
  font-size: 14px;
  color: #e0e6ed;
}

.workspace-status-path {
  font-size: 12px;
  color: #5a6a7a;
  font-family: monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@keyframes fadeIn {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}

/* Forms */
.form-row {
  display: flex;
  gap: 24px;
}

.flex-1 { flex: 1; }

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  color: #8899aa;
  font-size: 13px;
  font-weight: 600;
}

.form-help-text {
  font-size: 12px;
  color: #5a6a7a;
  margin-top: 4px;
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
  transition: border-color 0.15s ease;
  outline: none;
  box-sizing: border-box;
}

.input-field:focus {
  border-color: #4fc3f7;
}

.input-field:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  background: #151a24;
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
  gap: 8px;
  padding: 10px 16px;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #8899aa;
  cursor: pointer;
  transition: all 0.15s ease;
  font-family: inherit;
}

.mode-btn:hover {
  border-color: #3a4f5e;
  color: #ccd6e0;
}

.mode-btn.active {
  border-color: #4fc3f7;
  color: #4fc3f7;
  background: rgba(79, 195, 247, 0.08);
}

.mode-icon {
  display: flex;
  align-items: center;
}

.mode-label {
  font-size: 13px;
  font-weight: 600;
}

/* Path Selector */
.path-selector {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.path-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.path-tab {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 16px;
  color: #8899aa;
  cursor: pointer;
  font-size: 12px;
  font-family: inherit;
  transition: all 0.15s ease;
  max-width: 200px;
}

.path-tab:hover {
  border-color: #3a4f5e;
  color: #ccd6e0;
}

.path-tab.active {
  border-color: #4fc3f7;
  color: #4fc3f7;
  background: rgba(79, 195, 247, 0.08);
}

.path-tab-remove {
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.5;
  transition: opacity 0.15s, color 0.15s;
  padding: 2px;
  margin-right: -4px;
}

.path-tab-remove:hover {
  opacity: 1;
  color: #ef5350;
}

.path-input-row {
  display: flex;
  gap: 8px;
}

/* Shell Selector */
.shell-selector {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.shell-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.shell-tab {
  padding: 6px 14px;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 16px;
  color: #8899aa;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s;
}

.shell-tab:hover {
  border-color: #3a4f5e;
  color: #ccd6e0;
}

.shell-tab.active {
  border-color: #4fc3f7;
  background: rgba(79, 195, 247, 0.08);
  color: #4fc3f7;
}

.shell-input-row {
  display: flex;
}

/* Checkbox */
.checkbox-group {
  margin-top: 4px;
}

.checkbox-label {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  user-select: none;
  gap: 8px;
}

.checkbox-label input {
  width: 16px;
  height: 16px;
  accent-color: #4fc3f7;
  margin: 0;
  cursor: pointer;
}

.checkbox-text {
  color: #8899aa;
  font-size: 14px;
}

/* Buttons */
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 10px 20px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  border: 1px solid #2a2f3e;
  outline: none;
  background: #1a1f2e;
  color: #e0e6ed;
}

.btn:hover:not(:disabled) {
  background: #232a3b;
  border-color: #3a4f5e;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn.small {
  padding: 6px 14px;
  font-size: 13px;
}

.btn.primary {
  background: #4fc3f7;
  border-color: #4fc3f7;
  color: #0f1219;
}

.btn.primary:hover:not(:disabled) {
  background: #7bd4f9;
  border-color: #7bd4f9;
}

.btn.danger {
  background: transparent;
  color: #ef5350;
  border-color: #ef5350;
}

.btn.danger:hover:not(:disabled) {
  background: rgba(239, 83, 80, 0.1);
}

.btn.secondary {
  background: transparent;
  color: #8899aa;
}

.btn.secondary:hover:not(:disabled) {
  color: #e0e6ed;
  background: rgba(255, 255, 255, 0.05);
}

.icon-btn {
  width: 40px;
  min-width: 40px;
  padding: 0;
  color: #8899aa;
}

.icon-btn:hover:not(:disabled) {
  color: #4fc3f7;
}

/* Launch Actions */
.launch-actions {
  display: flex;
  gap: 12px;
  padding: 16px 24px;
  background: rgba(15, 18, 25, 0.4);
  border-top: 1px solid #2a2f3e;
}

.launch-btn {
  padding: 10px 24px;
}

/* Sessions Card */
.sessions-card {
  display: flex;
  flex-direction: column;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 24px;
  border-bottom: 1px solid #2a2f3e;
  background: rgba(15, 18, 25, 0.2);
}

.card-header h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

.sessions-content {
  display: flex;
  flex-direction: column;
  padding: 16px 20px;
  gap: 14px;
}

.session-tabs-container {
  overflow-x: auto;
  padding-bottom: 8px;
  margin-bottom: -8px; /* Offset for padding */
}

.session-tabs-container::-webkit-scrollbar {
  height: 6px;
}

.session-tabs-container::-webkit-scrollbar-track {
  background: rgba(15, 18, 25, 0.5);
  border-radius: 3px;
}

.session-tabs-container::-webkit-scrollbar-thumb {
  background: #2a2f3e;
  border-radius: 3px;
}

.session-tabs {
  display: flex;
  gap: 12px;
  min-width: max-content;
}

.session-tab {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 16px;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.15s ease;
  min-width: 160px;
}

.session-tab:hover {
  border-color: #3a4f5e;
}

.session-tab.selected {
  border-color: #4fc3f7;
  background: rgba(79, 195, 247, 0.05);
}

.session-tab.status-running { border-left: 3px solid #66bb6a; }
.session-tab.status-stopped { border-left: 3px solid #5a6a7a; }
.session-tab.status-exited { border-left: 3px solid #ffa726; }
.session-tab.status-failed { border-left: 3px solid #ef5350; }

.session-tab-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.session-mode-icon {
  display: flex;
  align-items: center;
  color: #8899aa;
}

.session-id {
  font-size: 13px;
  font-weight: 600;
  color: #e0e6ed;
}

.session-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  margin-left: auto;
}

.dot-running { background: #66bb6a; box-shadow: 0 0 6px rgba(102, 187, 106, 0.4); }
.dot-stopped { background: #5a6a7a; }
.dot-exited { background: #ffa726; }
.dot-failed { background: #ef5350; }

.session-tab-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.session-provider {
  font-size: 12px;
  color: #8899aa;
  font-weight: 600;
}

.session-model {
  font-size: 11px;
  color: #5a6a7a;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Session Detail */
.session-detail {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 14px 16px;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 6px 20px;
  margin-bottom: 10px;
}

.detail-item {
  display: flex;
  flex-direction: row;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
}

.detail-label {
  font-size: 12px;
  color: #5a6a7a;
  font-weight: 600;
  white-space: nowrap;
  flex-shrink: 0;
}

.detail-label::after {
  content: ':';
}

.detail-value {
  font-size: 13px;
  color: #e0e6ed;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.path-value {
  word-break: break-all;
  color: #8899aa;
  line-height: 1.4;
}

.text-running { color: #66bb6a; }
.text-stopped { color: #5a6a7a; }
.text-exited { color: #ffa726; }
.text-failed { color: #ef5350; }

.detail-actions {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
  padding-top: 10px;
  border-top: 1px solid #2a2f3e;
}
</style>
