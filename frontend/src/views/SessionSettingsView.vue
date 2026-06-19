<template>
  <section class="view-settings">
    <PageHead title="会话设置" description="配置并启动一个新的 AI 编程会话"/>
    <ConfigCard>
      <template #default>
        <div class="card-head">
          <h2>快速启动</h2>
        </div>
        <div class="segmented">
          <button class="seg active">ClaudeCode</button>
          <button class="seg">OpenCode</button>
          <button class="seg">Codex</button>
        </div>
        <div class="setting-list">
          <div class="setting-row">
            <label>服务提供商</label>
            <div class="dropdown">Anthropic <span class="caret">▾</span></div>
          </div>
          <div class="setting-row">
            <label>预设配置</label>
            <div class="dropdown">默认编码 <span class="caret">▾</span></div>
          </div>
          <div class="setting-row">
            <label>启动模式</label>
            <div class="dropdown">内嵌终端 <span class="caret">▾</span></div>
          </div>
          <div class="setting-row">
            <label>终端 Shell</label>
            <div class="dropdown">直接启动 <span class="caret">▾</span></div>
          </div>
          <div class="setting-row">
            <label>工作目录</label>
            <div class="input-group">
              <div class="text-input">
                <input value="/Users/maorun/projects/demo"/>
              </div>
              <button class="btn btn-ghost">浏览</button>
            </div>
          </div>
          <div class="setting-row">
            <label>启用注入代理</label>
            <div class="switch" @click="handleToggleSwitch">
              <div class="knob"></div>
            </div>
          </div>
        </div>
      </template>
    </ConfigCard>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'

const switchOn = ref(true)

function handleToggleSwitch() {
  switchOn.value = !switchOn.value
  console.log('toggleSwitch:', switchOn.value)
}
</script>

<style scoped>
.view-settings {
  padding: 32px 36px;
  gap: 22px;
  overflow: auto;
  display: flex;
  flex-direction: column;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-head h2 {
  font-size: 17px;
  font-weight: 600;
}

.segmented {
  display: flex;
  gap: 2px;
  background: var(--control);
  border-radius: 9px;
  padding: 3px;
}

.seg {
  flex: 1;
  padding: 7px 0;
  border: none;
  background: transparent;
  border-radius: 7px;
  cursor: pointer;
  font-size: 13px;
  color: var(--secondary);
  font-family: inherit;
  transition: all .12s;
}

.seg.active {
  background: var(--card);
  color: var(--label);
  box-shadow: 0 1px 2px rgba(0,0,0,.08);
}

.setting-list {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.setting-row:first-child {
  border-top: none;
}

.setting-row label {
  font-size: 14px;
  color: var(--secondary);
}

.dropdown {
  font-size: 13px;
  color: var(--label);
  cursor: pointer;
}

.caret {
  margin-left: 6px;
  color: var(--tertiary);
}

.input-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.text-input {
  background: var(--control);
  border-radius: 8px;
  padding: 0 12px;
  display: flex;
  align-items: center;
}

.text-input input {
  border: none;
  background: transparent;
  padding: 8px 0;
  font-size: 13px;
  color: var(--label);
  font-family: inherit;
  width: 280px;
  outline: none;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  padding: 9px 16px;
  transition: background .15s, box-shadow .15s;
  font-family: inherit;
}

.btn-primary {
  background: var(--accent);
  color: #fff;
}

.btn-primary:hover {
  background: var(--accentHover);
}

.btn-ghost {
  background: var(--control);
  color: var(--secondary);
}

.btn-ghost:hover {
  background: var(--controlHover);
}

.switch {
  width: 42px;
  height: 24px;
  background: var(--success);
  border-radius: 999px;
  position: relative;
  cursor: pointer;
  transition: background .15s;
}

.switch.off {
  background: var(--tertiary);
}

.knob {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 20px;
  height: 20px;
  background: #fff;
  border-radius: 50%;
  box-shadow: 0 1px 3px rgba(0,0,0,.15);
  transition: transform .15s;
}

.switch.off .knob {
  transform: translateX(-18px);
}
</style>
