<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'

const apiBase = '/api'

const repositories = ref([])
const credentials = ref([])
const busy = ref(false)
const editingRepoId = ref('')
const showRepoModal = ref(false)
const showCredentialModal = ref(false)
const showPublicKeyModal = ref(false)
const currentView = ref('repositories')
const toasts = ref([])
const syncingRepoIds = ref(new Set())
const createdSSHCredential = ref(null)
const repoPullTest = reactive({ state: 'idle', message: '' })
const repoCommitTest = reactive({ state: 'idle', message: '' })
const editPullTest = reactive({ state: 'idle', message: '' })
const editCommitTest = reactive({ state: 'idle', message: '' })

const repoForm = reactive({
  name: '',
  remoteUrl: '',
  branch: 'main',
  syncIntervalSec: 300,
  credentialId: '',
  autoCommitEnabled: true,
  autoPullEnabled: true,
  enabled: true
})

const credentialForm = reactive({
  name: '',
  type: 'ssh',
  matchUrl: '',
  username: '',
  password: ''
})

const editForm = reactive({
  name: '',
  remoteUrl: '',
  branch: 'main',
  syncIntervalSec: 300,
  credentialId: '',
  autoCommitEnabled: true,
  autoPullEnabled: true,
  enabled: true
})

const navItems = [
  { id: 'repositories', label: '仓库' },
  { id: 'credentials', label: '凭据' }
]

const filteredRepositories = computed(() => {
  return [...repositories.value]
    .sort((a, b) => a.repo.name.localeCompare(b.repo.name))
})

const filteredCredentials = computed(() => {
  return [...credentials.value]
    .sort((a, b) => a.name.localeCompare(b.name))
})

const editingRepo = computed(() => {
  return repositories.value.find((item) => item.repo.id === editingRepoId.value) || null
})

const viewTitle = computed(() => {
  return currentView.value === 'credentials' ? '凭据' : '仓库'
})

function showToast(message, type = 'info') {
  const id = `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
  toasts.value = [...toasts.value, { id, message, type }]
  setTimeout(() => {
    toasts.value = toasts.value.filter((toast) => toast.id !== id)
  }, 3200)
}

function setRepoSyncing(id, syncing) {
  const next = new Set(syncingRepoIds.value)
  if (syncing) {
    next.add(id)
  } else {
    next.delete(id)
  }
  syncingRepoIds.value = next
}

function isRepoSyncing(id) {
  return syncingRepoIds.value.has(id)
}

function formatTime(value) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '--'
  if (date.getFullYear() <= 1) return '--'
  const pad = (part) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function shortRevision(value) {
  if (!value) return '--'
  return String(value).slice(0, 7)
}

function runStatusSymbol(status) {
  if (status === 'success') return '✓'
  if (status === 'error') return '✕'
  return '--'
}

async function request(path, options = {}) {
  const response = await fetch(`${apiBase}${path}`, {
    headers: {
      'Content-Type': 'application/json'
    },
    ...options
  })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({ error: 'request failed' }))
    throw new Error(payload.error || 'request failed')
  }
  if (response.status === 204) {
    return null
  }
  return response.json()
}

async function runRepoTest(endpoint, payload, state) {
  state.state = 'testing'
  state.message = '检测中...'
  try {
    const result = await request(endpoint, {
      method: 'POST',
      body: JSON.stringify(payload)
    })
    state.state = normalizeTestState(result.state || (result.ok ? 'success' : 'error'), result.message)
    state.message = result.message
  } catch (error) {
    state.state = normalizeTestState('error', error.message)
    state.message = error.message
  }
}

function resetTestState(state) {
  state.state = 'idle'
  state.message = ''
}

function normalizeTestState(state, message) {
  if (typeof message === 'string' && message.includes('本地仓库不存在')) {
    return 'warning'
  }
  return state
}

function testSymbol(state) {
  if (state === 'success') return '✓'
  if (state === 'error') return '✕'
  if (state === 'warning') return '!'
  if (state === 'testing') return '…'
  return ''
}

function buildRepoPayload(form) {
  return {
    name: form.name,
    remoteUrl: form.remoteUrl,
    branch: form.branch,
    syncIntervalSec: form.syncIntervalSec,
    credentialId: form.credentialId,
    autoCommitEnabled: form.autoCommitEnabled,
    autoPullEnabled: form.autoPullEnabled,
    enabled: form.enabled
  }
}

function setupAutoTests(form, pullState, commitState) {
  let pullTimer = null
  let commitTimer = null

  watch(
    () => [form.name, form.remoteUrl, form.branch, form.credentialId, form.autoPullEnabled],
    () => {
      clearTimeout(pullTimer)
      resetTestState(pullState)
      if (!form.autoPullEnabled || !form.name || !form.remoteUrl || !form.branch) {
        return
      }
      pullTimer = setTimeout(() => {
        runRepoTest('/repositories/test-pull', buildRepoPayload(form), pullState)
      }, 600)
    },
    { deep: false }
  )

  watch(
    () => [form.name, form.remoteUrl, form.branch, form.credentialId, form.autoCommitEnabled],
    () => {
      clearTimeout(commitTimer)
      resetTestState(commitState)
      if (!form.autoCommitEnabled || !form.name) {
        return
      }
      commitTimer = setTimeout(() => {
        runRepoTest('/repositories/test-commit', buildRepoPayload(form), commitState)
      }, 600)
    },
    { deep: false }
  )
}

async function loadAll() {
  busy.value = true
  try {
    const [repoList, credList] = await Promise.all([
      request('/repositories'),
      request('/credentials')
    ])
    repositories.value = repoList
    credentials.value = credList
    if (editingRepoId.value && !repoList.some((item) => item.repo.id === editingRepoId.value)) {
      editingRepoId.value = ''
    }
  } catch (error) {
    showToast(error.message, 'error')
  } finally {
    busy.value = false
  }
}

async function refreshRepositories() {
  busy.value = true
  try {
    const result = await request('/repositories/scan', { method: 'POST' })
    await loadAll()
    if (result.imported > 0) {
      showToast(`已导入 ${result.imported} 个本地仓库`, 'success')
    } else {
      showToast('未发现新的本地仓库', 'info')
    }
  } catch (error) {
    showToast(error.message, 'error')
  } finally {
    busy.value = false
  }
}

function resetRepoForm() {
  repoForm.name = ''
  repoForm.remoteUrl = ''
  repoForm.branch = 'main'
  repoForm.syncIntervalSec = 300
  repoForm.credentialId = ''
  repoForm.autoCommitEnabled = true
  repoForm.autoPullEnabled = true
  repoForm.enabled = true
  resetTestState(repoPullTest)
  resetTestState(repoCommitTest)
}

function resetCredentialForm() {
  credentialForm.name = ''
  credentialForm.type = 'ssh'
  credentialForm.matchUrl = ''
  credentialForm.username = ''
  credentialForm.password = ''
}

async function createCredential() {
  try {
    const created = await request('/credentials', {
      method: 'POST',
      body: JSON.stringify(credentialForm)
    })
    resetCredentialForm()
    showCredentialModal.value = false
    currentView.value = 'credentials'
    await loadAll()
    if (created.type === 'ssh' && created.publicKey) {
      createdSSHCredential.value = created
      showPublicKeyModal.value = true
    }
    showToast('凭据已保存', 'success')
  } catch (error) {
    showToast(error.message, 'error')
  }
}

async function removeCredential(id) {
  try {
    await request(`/credentials/${id}`, { method: 'DELETE' })
    if (createdSSHCredential.value?.id === id) {
      createdSSHCredential.value = null
      showPublicKeyModal.value = false
    }
    await loadAll()
    showToast('凭据已删除', 'success')
  } catch (error) {
    showToast(error.message, 'error')
  }
}

function openPublicKeyModal(credential) {
  createdSSHCredential.value = credential
  showPublicKeyModal.value = true
}

async function copyPublicKey() {
  if (!createdSSHCredential.value?.publicKey) {
    return
  }
  try {
    await navigator.clipboard.writeText(createdSSHCredential.value.publicKey)
    showToast('公钥已复制', 'success')
  } catch (error) {
    showToast('复制失败', 'error')
  }
}

async function createRepository() {
  try {
    await request('/repositories', {
      method: 'POST',
      body: JSON.stringify(repoForm)
    })
    resetRepoForm()
    showRepoModal.value = false
    currentView.value = 'repositories'
    await loadAll()
    showToast('仓库已添加并开始监控', 'success')
  } catch (error) {
    showToast(error.message, 'error')
  }
}

async function triggerSync(id) {
  setRepoSyncing(id, true)
  try {
    await request(`/repositories/${id}/sync`, { method: 'POST' })
    await loadAll()
    showToast('同步完成', 'success')
  } catch (error) {
    showToast(error.message, 'error')
  } finally {
    setRepoSyncing(id, false)
  }
}

async function removeRepository(id) {
  try {
    await request(`/repositories/${id}`, { method: 'DELETE' })
    if (editingRepoId.value === id) {
      editingRepoId.value = ''
    }
    await loadAll()
    showToast('仓库已移除', 'success')
  } catch (error) {
    showToast(error.message, 'error')
  }
}

function beginEdit(repo) {
  editingRepoId.value = repo.id
  editForm.name = repo.name
  editForm.remoteUrl = repo.remoteUrl
  editForm.branch = repo.branch
  editForm.syncIntervalSec = repo.syncIntervalSec
  editForm.credentialId = repo.credentialId || ''
  editForm.autoCommitEnabled = repo.autoCommitEnabled
  editForm.autoPullEnabled = repo.autoPullEnabled ?? true
  editForm.enabled = repo.enabled
  resetTestState(editPullTest)
  resetTestState(editCommitTest)
}

function cancelEdit() {
  editingRepoId.value = ''
}

async function saveEdit(id) {
  try {
    await request(`/repositories/${id}`, {
      method: 'PUT',
      body: JSON.stringify(editForm)
    })
    editingRepoId.value = ''
    await loadAll()
    showToast('仓库配置已更新', 'success')
  } catch (error) {
    showToast(error.message, 'error')
  }
}

onMounted(loadAll)
setupAutoTests(repoForm, repoPullTest, repoCommitTest)
setupAutoTests(editForm, editPullTest, editCommitTest)
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">GTI</div>
        <div>
          <strong>GTI Monitor</strong>
          <p>Git 同步控制中心</p>
        </div>
      </div>

      <nav class="nav">
        <button
          v-for="item in navItems"
          :key="item.id"
          :class="['nav-item', currentView === item.id ? 'active' : '']"
          type="button"
          @click="currentView = item.id"
        >
          {{ item.label }}
        </button>
      </nav>
    </aside>

    <main class="workspace">
      <header class="topbar simple-topbar">
        <h1>{{ viewTitle }}</h1>
        <div class="topbar-actions">
          <button class="secondary" @click="currentView === 'repositories' ? refreshRepositories() : loadAll()">刷新数据</button>
          <button v-if="currentView === 'repositories'" @click="showRepoModal = true">新增仓库</button>
          <button v-if="currentView === 'credentials'" @click="showCredentialModal = true">新增凭据</button>
        </div>
      </header>

      <template v-if="currentView === 'repositories'">
        <section class="list-surface">
          <div class="table-wrap flat-table">
            <table class="repo-table">
              <thead>
                <tr>
                  <th>仓库</th>
                  <th>分支</th>
                  <th>Commit</th>
                  <th>周期</th>
                  <th>Push</th>
                  <th>Pull</th>
                  <th class="repo-actions-header">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in filteredRepositories" :key="item.repo.id">
                  <td>
                    <strong>{{ item.repo.name }}</strong>
                  </td>
                  <td>{{ item.repo.branch }}</td>
                  <td><code class="commit-hash">{{ shortRevision(item.repo.lastRevision || item.lastRun?.repositoryRevision) }}</code></td>
                  <td>{{ item.repo.syncIntervalSec }} 秒</td>
                  <td>
                    <div class="op-status-cell">
                      <span>{{ formatTime(item.repo.lastPushAt) }}</span>
                      <span :class="['status-symbol', item.repo.lastPushStatus || 'idle']">{{ runStatusSymbol(item.repo.lastPushStatus) }}</span>
                    </div>
                  </td>
                  <td>
                    <div class="op-status-cell">
                      <span>{{ formatTime(item.repo.lastPullAt) }}</span>
                      <span :class="['status-symbol', item.repo.lastPullStatus || 'idle']">{{ runStatusSymbol(item.repo.lastPullStatus) }}</span>
                    </div>
                  </td>
                  <td class="repo-actions-cell">
                    <div class="table-actions repo-actions">
                      <button class="secondary slim repo-action-button sync-button" :class="{ loading: isRepoSyncing(item.repo.id) }" :disabled="isRepoSyncing(item.repo.id)" @click="triggerSync(item.repo.id)">
                        <span class="sync-slot left">
                          <span v-if="isRepoSyncing(item.repo.id)" class="spinner" />
                        </span>
                        <span class="sync-label">{{ isRepoSyncing(item.repo.id) ? '同步中' : '同步' }}</span>
                        <span class="sync-slot right" aria-hidden="true" />
                      </button>
                      <button class="secondary slim repo-action-button" @click="beginEdit(item.repo)">编辑</button>
                      <button class="danger slim repo-action-button" @click="removeRepository(item.repo.id)">删除</button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!filteredRepositories.length">
                  <td colspan="7" class="empty-row">暂无符合条件的仓库</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>

      <template v-else>
        <section class="list-surface">
          <div class="table-wrap flat-table">
            <table class="repo-table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>类型</th>
                  <th>说明</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="cred in filteredCredentials" :key="cred.id">
                  <td><strong>{{ cred.name }}</strong></td>
                  <td><span class="badge">{{ cred.type }}</span></td>
                  <td>
                    <div class="credential-meta">
                      <span>{{ cred.type === 'http' ? cred.matchUrl : cred.maskedSecretHint }}</span>
                    </div>
                  </td>
                  <td>
                    <div class="table-actions">
                      <button v-if="cred.type === 'ssh' && cred.publicKey" class="secondary slim" @click="openPublicKeyModal(cred)">查看公钥</button>
                      <button class="danger slim" @click="removeCredential(cred.id)">删除</button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!filteredCredentials.length">
                  <td colspan="4" class="empty-row">暂无符合条件的凭据</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </main>

    <div v-if="showRepoModal" class="modal-backdrop" @click.self="showRepoModal = false">
      <div class="modal-card">
        <div class="modal-head">
          <h2>新增仓库</h2>
        </div>
        <div class="form-grid">
          <label class="field-row span-2">
            <span class="field-label">仓库名称:</span>
            <input v-model="repoForm.name" placeholder="例如 my-service" />
          </label>
          <label class="field-row span-2">
            <span class="field-label">仓库地址:</span>
            <input v-model="repoForm.remoteUrl" placeholder="git@github.com:owner/repo.git" />
          </label>
          <label class="field-row">
            <span class="field-label">分支:</span>
            <input v-model="repoForm.branch" placeholder="main" />
          </label>
          <label class="field-row">
            <span class="field-label">凭据:</span>
            <select v-model="repoForm.credentialId">
              <option value="">无</option>
              <option v-for="cred in credentials" :key="cred.id" :value="cred.id">
                {{ cred.name }} / {{ cred.type }}{{ cred.matchUrl ? ` / ${cred.matchUrl}` : '' }}
              </option>
            </select>
          </label>
          <div class="field-row field-row-inline span-2">
            <span class="field-label">监控周期:</span>
            <input v-model.number="repoForm.syncIntervalSec" type="number" min="30" class="period-input" />
            <span class="field-unit">秒</span>
            <label class="switch-inline">
              <span>自动提交:</span>
              <input v-model="repoForm.autoCommitEnabled" type="checkbox" />
              <span v-if="repoCommitTest.state !== 'idle'" :class="['test-state', repoCommitTest.state]" :title="repoCommitTest.message">
                {{ testSymbol(repoCommitTest.state) }}
              </span>
            </label>
            <label class="switch-inline">
              <span>自动拉取:</span>
              <input v-model="repoForm.autoPullEnabled" type="checkbox" />
              <span v-if="repoPullTest.state !== 'idle'" :class="['test-state', repoPullTest.state]" :title="repoPullTest.message">
                {{ testSymbol(repoPullTest.state) }}
              </span>
            </label>
          </div>
        </div>
        <div class="modal-actions">
          <button class="secondary" @click="showRepoModal = false">取消</button>
          <button @click="createRepository">添加</button>
        </div>
      </div>
    </div>

    <div v-if="showCredentialModal" class="modal-backdrop" @click.self="showCredentialModal = false">
      <div class="modal-card">
        <div class="modal-head">
          <h2>新增凭据</h2>
        </div>
        <div class="form-grid">
          <label>
            名称
            <input v-model="credentialForm.name" placeholder="例如 GitHub SSH" />
          </label>
          <label>
            类型
            <select v-model="credentialForm.type">
              <option value="ssh">SSH 密钥对</option>
              <option value="http">用户名 / Token</option>
            </select>
          </label>
          <label v-if="credentialForm.type === 'http'" class="span-2">
            匹配地址
            <input v-model="credentialForm.matchUrl" placeholder="例如 https://github.com 或 github.com/org/" />
          </label>
          <label v-if="credentialForm.type === 'http'">
            用户名
            <input v-model="credentialForm.username" placeholder="用户名" />
          </label>
          <label v-if="credentialForm.type === 'http'">
            密码或 Token
            <input v-model="credentialForm.password" placeholder="Token 或密码" />
          </label>
          <div v-if="credentialForm.type === 'ssh'" class="ssh-create-note span-2">
            创建后系统会自动生成 SSH 密钥对，并展示可复制的公钥。
          </div>
        </div>
        <div class="modal-actions">
          <button class="secondary" @click="showCredentialModal = false">取消</button>
          <button @click="createCredential">添加</button>
        </div>
      </div>
    </div>

    <div v-if="showPublicKeyModal && createdSSHCredential" class="modal-backdrop" @click.self="showPublicKeyModal = false">
      <div class="modal-card public-key-card">
        <div class="modal-head">
          <h2>SSH 公钥</h2>
        </div>
        <div class="public-key-body">
          <p>{{ createdSSHCredential.name }}</p>
          <textarea :value="createdSSHCredential.publicKey" rows="6" readonly class="public-key-output" />
        </div>
        <div class="modal-actions">
          <button class="secondary" @click="showPublicKeyModal = false">关闭</button>
          <button @click="copyPublicKey">复制公钥</button>
        </div>
      </div>
    </div>

    <div v-if="editingRepo" class="modal-backdrop" @click.self="cancelEdit">
      <div class="modal-card">
        <div class="modal-head">
          <h2>编辑仓库</h2>
        </div>
        <div class="form-grid">
          <label class="field-row span-2">
            <span class="field-label">仓库名称:</span>
            <input v-model="editForm.name" disabled class="readonly-input" />
          </label>
          <label class="field-row span-2">
            <span class="field-label">仓库地址:</span>
            <input v-model="editForm.remoteUrl" disabled class="readonly-input" />
          </label>
          <label class="field-row">
            <span class="field-label">分支:</span>
            <input v-model="editForm.branch" />
          </label>
          <label class="field-row">
            <span class="field-label">凭据:</span>
            <select v-model="editForm.credentialId">
              <option value="">无</option>
              <option v-for="cred in credentials" :key="cred.id" :value="cred.id">
                {{ cred.name }} / {{ cred.type }}{{ cred.matchUrl ? ` / ${cred.matchUrl}` : '' }}
              </option>
            </select>
          </label>
          <div class="field-row field-row-inline span-2">
            <span class="field-label">监控周期:</span>
            <input v-model.number="editForm.syncIntervalSec" type="number" min="30" class="period-input" />
            <span class="field-unit">秒</span>
            <label class="switch-inline">
              <span>自动提交:</span>
              <input v-model="editForm.autoCommitEnabled" type="checkbox" />
              <span v-if="editCommitTest.state !== 'idle'" :class="['test-state', editCommitTest.state]" :title="editCommitTest.message">
                {{ testSymbol(editCommitTest.state) }}
              </span>
            </label>
            <label class="switch-inline">
              <span>自动拉取:</span>
              <input v-model="editForm.autoPullEnabled" type="checkbox" />
              <span v-if="editPullTest.state !== 'idle'" :class="['test-state', editPullTest.state]" :title="editPullTest.message">
                {{ testSymbol(editPullTest.state) }}
              </span>
            </label>
          </div>
        </div>
        <div class="modal-actions">
          <button class="secondary" @click="cancelEdit">取消</button>
          <button @click="saveEdit(editingRepo.repo.id)">保存</button>
        </div>
      </div>
    </div>

    <div v-if="toasts.length" class="toast-stack">
      <div v-for="toast in toasts" :key="toast.id" :class="['toast', toast.type]">
        {{ toast.message }}
      </div>
    </div>
  </div>
</template>
