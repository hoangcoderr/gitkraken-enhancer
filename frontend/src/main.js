import './style.css';
import { DetectAsar, GetPatches, ApplyPatch, BrowseAsar } from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';

function cssEscape(s) {
  if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') return CSS.escape(s);
  return String(s).replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

/** @typedef {{ path: string, version: string, size: number }} AsarInfo */
/** @typedef {{ version: string, patches: Array<{file:string,find:string,replace:string}> }} PatchFile */
/** @typedef {{ success: boolean, message: string, newSize?: number, path?: string, version?: string }} PatchResult */

let patches = /** @type {PatchFile[]} */ ([]);
/** @type {AsarInfo|null} */ let autoAsar = null;
/** @type {PatchFile|null} */ let autoPatch = null;
/** @type {AsarInfo|null} */ let customAsar = null;
/** @type {PatchFile|null} */ let customPatch = null;
/** @type {Set<string>} */ let patchedPaths = new Set();
let applying = false;
/** @type {'auto'|'custom'} */ let activeTab = 'auto';

const $ = (id) => document.getElementById(id);

// ── Helpers ──────────────────────────────────────────────────

function formatSize(bytes) {
  if (bytes == null || Number.isNaN(bytes)) return '—';
  const n = Number(bytes);
  if (n >= 1073741824) return (n / 1073741824).toFixed(2) + ' GB';
  if (n >= 1048576) return (n / 1048576).toFixed(1) + ' MB';
  if (n >= 1024) return (n / 1024).toFixed(0) + ' KB';
  return n + ' B';
}

function nowTime() {
  const d = new Date();
  return d.toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

/**
 * Append a log line. Always updates the DOM immediately.
 * @param {string} msg
 * @param {'ok'|'err'|'info'|'warn'|''} [type]
 * @param {'auto'|'custom'} [tab]
 */
function log(msg, type = '', tab = activeTab) {
  const el = $(tab === 'custom' ? 'log-custom' : 'log-auto');
  if (!el) return;

  // Remove empty placeholder
  const empty = el.querySelector('.log-empty');
  if (empty) empty.remove();

  const line = document.createElement('div');
  line.className = 'log-line' + (type ? ' ' + type : '');
  line.innerHTML = `<span class="ts">${nowTime()}</span><span class="msg">${escapeHtml(msg)}</span>`;
  el.appendChild(line);

  // Keep last ~300 lines
  while (el.children.length > 300) el.removeChild(el.firstChild);

  // Force layout + scroll (rAF so paint happens even under load)
  requestAnimationFrame(() => {
    el.scrollTop = el.scrollHeight;
  });
}

function clearLog(tab) {
  const el = $(tab === 'custom' ? 'log-custom' : 'log-auto');
  if (!el) return;
  el.innerHTML = '<div class="log-empty">Console cleared. Ready.</div>';
}

function setProgress(tab, { visible, percent = 0, label = '', indeterminate = false }) {
  const wrap = $(tab === 'custom' ? 'progress-custom' : 'progress-auto');
  const fill = $(tab === 'custom' ? 'progress-custom-fill' : 'progress-auto-fill');
  const pct = $(tab === 'custom' ? 'progress-custom-pct' : 'progress-auto-pct');
  const lab = $(tab === 'custom' ? 'progress-custom-label' : 'progress-auto-label');
  if (!wrap || !fill || !pct || !lab) return;

  if (!visible) {
    wrap.hidden = true;
    wrap.dataset.indeterminate = 'false';
    fill.style.width = '0%';
    return;
  }
  wrap.hidden = false;
  wrap.dataset.indeterminate = indeterminate ? 'true' : 'false';
  const p = Math.max(0, Math.min(100, Number(percent) || 0));
  fill.style.width = p + '%';
  pct.textContent = Math.round(p) + '%';
  if (label) lab.textContent = label;
}

function updateAutoBtn() {
  const btn = $('btn-auto');
  if (!btn) return;
  btn.disabled = applying || !(autoAsar && autoPatch);
  if (!applying) btn.textContent = 'Apply Patch';
}

function updateCustomBtn() {
  const btn = $('btn-custom');
  if (!btn) return;
  btn.disabled = applying || !(customAsar && customPatch);
  if (!applying) btn.textContent = 'Apply Patch';
}

function setSelectionStatus(state, text) {
  const el = $('selectionStatus');
  if (!el) return;
  el.dataset.state = state;
  el.textContent = text;
}

function renderSelection() {
  const body = $('selectionBody');
  if (!body) return;

  if (!autoAsar) {
    body.className = 'selection-body empty';
    body.innerHTML = `
      <div class="empty-state">
        <div class="empty-icon">◎</div>
        <p>Select an installation with a ready patch</p>
      </div>`;
    setSelectionStatus('idle', 'Idle');
    return;
  }

  const patchReady = !!autoPatch;
  const isPatched = patchedPaths.has(autoAsar.path);
  body.className = 'selection-body';
  body.innerHTML = `
    <div class="sel-grid">
      <div class="sel-field">
        <label>Version</label>
        <div class="val">v${escapeHtml(autoAsar.version || '?')}</div>
      </div>
      <div class="sel-field">
        <label>Size</label>
        <div class="val mono" id="selectionSize">${formatSize(autoAsar.size)}</div>
      </div>
      <div class="sel-field">
        <label>Patch</label>
        <div class="val">${patchReady ? 'v' + escapeHtml(autoPatch.version) + ' · ' + autoPatch.patches.length + ' edit(s)' : 'Not available'}</div>
      </div>
      <div class="sel-field">
        <label>State</label>
        <div class="val">${isPatched ? 'Patched this session' : (patchReady ? 'Ready to apply' : 'No patch')}</div>
      </div>
      <div class="sel-field full">
        <label>Path</label>
        <div class="val mono">${escapeHtml(autoAsar.path)}</div>
      </div>
    </div>`;

  if (applying) setSelectionStatus('busy', 'Patching…');
  else if (isPatched) setSelectionStatus('done', 'Patched');
  else if (patchReady) setSelectionStatus('ready', 'Ready');
  else setSelectionStatus('error', 'No patch');
}

function updateSelectionSize(size) {
  const el = $('selectionSize');
  if (el) el.textContent = formatSize(size);
}

function renderAsarInfo() {
  const el = $('asarInfo');
  if (!el) return;
  if (!customAsar) {
    el.className = 'asar-info empty';
    el.innerHTML = '<span class="muted">No file selected</span>';
    return;
  }
  el.className = 'asar-info';
  el.innerHTML = `
    <div class="sel-grid">
      <div class="sel-field">
        <label>Version</label>
        <div class="val">v${escapeHtml(customAsar.version || '?')}</div>
      </div>
      <div class="sel-field">
        <label>Size</label>
        <div class="val mono" id="customSize">${formatSize(customAsar.size)}</div>
      </div>
      <div class="sel-field full">
        <label>Path</label>
        <div class="val mono">${escapeHtml(customAsar.path)}</div>
      </div>
    </div>`;
}

// ── Tabs ─────────────────────────────────────────────────────

function switchTab(name) {
  activeTab = name === 'custom' ? 'custom' : 'auto';
  document.querySelectorAll('.tab').forEach((t) => {
    const on = t.dataset.tab === activeTab;
    t.classList.toggle('active', on);
    t.setAttribute('aria-selected', on ? 'true' : 'false');
  });
  document.querySelectorAll('.tab-panel').forEach((p) => {
    const on = p.id === 'tab-' + activeTab;
    p.classList.toggle('active', on);
    if (on) p.removeAttribute('hidden');
    else p.setAttribute('hidden', '');
  });
}

// ── Lists ────────────────────────────────────────────────────

function makeItem({ version, size, path, tagHtml, selected, disabled, onClick }) {
  const div = document.createElement('div');
  div.className = 'item' + (selected ? ' selected' : '') + (disabled ? ' disabled' : '');
  div.innerHTML = `
    <div class="item-main">
      <div class="item-title">
        <span class="ver">v${escapeHtml(version || '?')}</span>
        <span class="item-size" data-size-for="${escapeHtml(path || '')}">${formatSize(size)}</span>
      </div>
      <div class="item-meta" title="${escapeHtml(path || '')}">${escapeHtml(path || '')}</div>
    </div>
    ${tagHtml || ''}`;
  if (onClick && !disabled) div.onclick = onClick;
  return div;
}

async function loadPatches() {
  try {
    patches = (await GetPatches()) || [];
  } catch (e) {
    patches = [];
    log('Failed to load patches: ' + (e?.message || e), 'err', 'auto');
  }

  const pill = $('patchCountPill');
  if (pill) pill.textContent = patches.length + ' patch' + (patches.length === 1 ? '' : 'es');

  const list = $('patchList');
  if (!list) return;
  list.innerHTML = '';
  if (!patches.length) {
    list.innerHTML = '<div class="loading-row">No patch definitions found</div>';
    return;
  }
  patches.forEach((p) => {
    const selected = customPatch && customPatch.version === p.version;
    const item = makeItem({
      version: p.version,
      size: null,
      path: p.patches.length + ' edit' + (p.patches.length === 1 ? '' : 's') + ' · render.bundle.js',
      tagHtml: '<span class="tag tag-ok">Ready</span>',
      selected,
      onClick: () => {
        customPatch = p;
        document.querySelectorAll('#patchList .item').forEach((e) => e.classList.remove('selected'));
        item.classList.add('selected');
        log('Selected patch definition v' + p.version, 'info', 'custom');
        updateCustomBtn();
      },
    });
    // override size display for patch defs
    const sizeEl = item.querySelector('.item-size');
    if (sizeEl) sizeEl.textContent = p.patches.length + '×';
    list.appendChild(item);
  });
}

/**
 * @param {AsarInfo[]} dir
 * @param {HTMLElement} container
 * @param {'auto'|'custom'} mode
 */
function renderInstallations(dir, container, mode) {
  container.innerHTML = '';
  if (!dir.length) {
    container.innerHTML = `
      <div class="loading-row">
        No GitKraken installations found
      </div>`;
    return;
  }

  let firstSelectable = null;

  for (const item of dir) {
    const patch = patches.find((p) => p.version === item.version) || null;
    const isPatched = patchedPaths.has(item.path);
    let tagHtml;
    if (isPatched) tagHtml = '<span class="tag tag-done">Patched</span>';
    else if (patch) tagHtml = '<span class="tag tag-ok">Patch ready</span>';
    else tagHtml = '<span class="tag tag-wait">No patch</span>';

    const selected =
      mode === 'auto'
        ? !!(autoAsar && autoAsar.path === item.path)
        : !!(customAsar && customAsar.path === item.path);

    const row = makeItem({
      version: item.version,
      size: item.size,
      path: item.path,
      tagHtml,
      selected,
      disabled: mode === 'auto' && !patch,
      onClick: () => {
        if (mode === 'auto') {
          if (!patch) {
            log('No patch for v' + item.version + ' yet', 'warn', 'auto');
            return;
          }
          autoAsar = { ...item };
          autoPatch = patch;
          document.querySelectorAll('#detectedList .item').forEach((e) => e.classList.remove('selected'));
          row.classList.add('selected');
          log('Selected v' + item.version + ' · ' + formatSize(item.size), 'info', 'auto');
          renderSelection();
          updateAutoBtn();
        } else {
          customAsar = { ...item };
          document.querySelectorAll('#detectedCustom .item').forEach((e) => e.classList.remove('selected'));
          row.classList.add('selected');
          renderAsarInfo();
          log('Loaded v' + (item.version || '?') + ' · ' + formatSize(item.size), 'info', 'custom');
          updateCustomBtn();
        }
      },
    });

    if (mode === 'auto' && patch && !firstSelectable) firstSelectable = { item, patch, row };
    container.appendChild(row);
  }

  // Auto-select first patchable if nothing selected
  if (mode === 'auto' && !autoAsar && firstSelectable) {
    autoAsar = { ...firstSelectable.item };
    autoPatch = firstSelectable.patch;
    firstSelectable.row.classList.add('selected');
    renderSelection();
    updateAutoBtn();
  }
}

async function scan(opts = { quiet: false }) {
  const c = $('detectedList');
  if (!c) return;
  c.innerHTML = `<div class="loading-row"><div class="spinner"></div> Scanning…</div>`;
  if (!opts.quiet) log('Scanning for GitKraken installations…', 'info', 'auto');

  let dir = [];
  try {
    dir = (await DetectAsar()) || [];
  } catch (e) {
    c.innerHTML = `<div class="loading-row">Scan failed</div>`;
    log('Scan failed: ' + (e?.message || e), 'err', 'auto');
    return;
  }

  // Preserve selection path if still present
  const prevPath = autoAsar?.path;
  if (prevPath) {
    const fresh = dir.find((d) => d.path === prevPath);
    if (fresh) {
      autoAsar = { ...fresh };
      autoPatch = patches.find((p) => p.version === fresh.version) || autoPatch;
    }
  }

  renderInstallations(dir, c, 'auto');
  renderSelection();
  updateAutoBtn();

  if (!opts.quiet) {
    const ready = dir.filter((d) => patches.some((p) => p.version === d.version)).length;
    log(`Found ${dir.length} install(s), ${ready} with patch available`, ready ? 'ok' : 'warn', 'auto');
  }
}

// ── Actions ──────────────────────────────────────────────────

async function browseAuto() {
  if (applying) return;
  log('Opening file dialog…', 'info', 'auto');
  let r;
  try {
    r = await BrowseAsar();
  } catch (e) {
    log('Browse failed: ' + (e?.message || e), 'err', 'auto');
    return;
  }
  if (!r || !r.path) {
    log('Browse cancelled', 'warn', 'auto');
    return;
  }
  autoAsar = { path: r.path, version: r.version, size: r.size };
  autoPatch = patches.find((p) => p.version === r.version) || null;
  log(
    `Loaded v${r.version || '?'} · ${formatSize(r.size)}` + (autoPatch ? ' · patch found' : ' · no matching patch'),
    autoPatch ? 'ok' : 'err',
    'auto'
  );
  // Re-scan list to reflect, but keep selection
  await scan({ quiet: true });
  // Force selection back to browsed file (may not be in detect list)
  autoAsar = { path: r.path, version: r.version, size: r.size };
  autoPatch = patches.find((p) => p.version === r.version) || null;
  renderSelection();
  updateAutoBtn();
}

async function browseCustom() {
  if (applying) return;
  log('Opening file dialog…', 'info', 'custom');
  let r;
  try {
    r = await BrowseAsar();
  } catch (e) {
    log('Browse failed: ' + (e?.message || e), 'err', 'custom');
    return;
  }
  if (!r || !r.path) {
    log('Browse cancelled', 'warn', 'custom');
    return;
  }
  customAsar = { path: r.path, version: r.version, size: r.size };
  renderAsarInfo();
  log(`Loaded v${r.version || '?'} · ${formatSize(r.size)}`, 'ok', 'custom');
  updateCustomBtn();
}

async function detectCustom() {
  if (applying) return;
  const c = $('detectedCustom');
  c.innerHTML = `<div class="loading-row"><div class="spinner"></div> Scanning…</div>`;
  log('Detecting installations…', 'info', 'custom');
  let list = [];
  try {
    list = (await DetectAsar()) || [];
  } catch (e) {
    c.innerHTML = '';
    log('Detect failed: ' + (e?.message || e), 'err', 'custom');
    return;
  }
  renderInstallations(list, c, 'custom');
  log(`Found ${list.length} installation(s)`, list.length ? 'ok' : 'warn', 'custom');
}

/**
 * @param {'auto'|'custom'} tab
 */
async function applyPatch(tab) {
  if (applying) return;
  const asar = tab === 'auto' ? autoAsar : customAsar;
  const patch = tab === 'auto' ? autoPatch : customPatch;
  if (!asar || !patch) return;

  applying = true;
  const btn = $(tab === 'auto' ? 'btn-auto' : 'btn-custom');
  if (btn) {
    btn.disabled = true;
    btn.classList.add('busy');
    btn.textContent = 'Applying…';
  }
  updateAutoBtn();
  updateCustomBtn();
  if (tab === 'auto') setSelectionStatus('busy', 'Patching…');

  setProgress(tab, { visible: true, percent: 3, label: 'Starting…', indeterminate: false });
  log(`Applying patch v${patch.version} → ${asar.path}`, 'info', tab);
  log(`Current size: ${formatSize(asar.size)}`, 'info', tab);

  /** @type {PatchResult|null} */
  let result = null;
  try {
    result = await ApplyPatch(asar.path, patch);
  } catch (e) {
    result = { success: false, message: String(e?.message || e) };
  }

  applying = false;
  if (btn) {
    btn.classList.remove('busy');
    btn.textContent = 'Apply Patch';
  }

  if (result?.success) {
    const newSize = result.newSize != null ? Number(result.newSize) : null;
    log(result.message || 'Patch applied successfully', 'ok', tab);
    if (newSize != null) {
      log(`Size updated: ${formatSize(asar.size)} → ${formatSize(newSize)}`, 'ok', tab);
      if (tab === 'auto' && autoAsar) {
        autoAsar = { ...autoAsar, size: newSize };
        updateSelectionSize(newSize);
      }
      if (tab === 'custom' && customAsar) {
        customAsar = { ...customAsar, size: newSize };
        const sizeEl = $('customSize');
        if (sizeEl) sizeEl.textContent = formatSize(newSize);
      }
      // Update any list row showing this path
      document.querySelectorAll(`[data-size-for="${cssEscape(asar.path)}"]`).forEach((el) => {
        el.textContent = formatSize(newSize);
      });
    }
    patchedPaths.add(asar.path);
    setProgress(tab, { visible: true, percent: 100, label: 'Done' });
    setTimeout(() => setProgress(tab, { visible: false }), 1600);

    // Rescan to refresh sizes from disk
    try {
      await scan({ quiet: true });
      if (tab === 'custom') await detectCustom();
    } catch (_) { /* ignore */ }

    if (tab === 'auto') {
      // re-bind selection after rescan
      if (autoAsar) {
        const still = autoAsar;
        autoAsar = { ...still, size: newSize != null ? newSize : still.size };
      }
      renderSelection();
    } else {
      renderAsarInfo();
    }
  } else {
    log(result?.message || 'Patch failed', 'err', tab);
    setProgress(tab, { visible: false });
    if (tab === 'auto') setSelectionStatus('error', 'Failed');
  }

  updateAutoBtn();
  updateCustomBtn();
  if (tab === 'auto' && result?.success) setSelectionStatus('done', 'Patched');
}

// ── Progress events from Go ──────────────────────────────────

function bindProgressEvents() {
  try {
    EventsOn('patch:progress', (payload) => {
      // payload may already be object
      const data = typeof payload === 'string' ? JSON.parse(payload) : payload || {};
      const stage = data.stage || '';
      const message = data.message || stage;
      const percent = Number(data.percent) || 0;
      const tab = activeTab;

      if (stage === 'error') {
        setProgress(tab, { visible: false });
        log(message, 'err', tab);
        return;
      }
      if (stage === 'done') {
        setProgress(tab, { visible: true, percent: 100, label: message });
        // final success log is handled by applyPatch result
        return;
      }
      setProgress(tab, {
        visible: true,
        percent,
        label: message,
        indeterminate: stage === 'extract' || stage === 'pack',
      });
      log(message, 'info', tab);
    });
  } catch (e) {
    console.warn('EventsOn failed', e);
  }
}

// ── Wire DOM ─────────────────────────────────────────────────

function wire() {
  document.querySelectorAll('.tab').forEach((tab) => {
    tab.addEventListener('click', () => switchTab(tab.dataset.tab));
  });

  $('btn-rescan')?.addEventListener('click', () => scan());
  $('btn-browse-auto')?.addEventListener('click', browseAuto);
  $('btn-browse-custom')?.addEventListener('click', browseCustom);
  $('btn-detect-custom')?.addEventListener('click', detectCustom);
  $('btn-auto')?.addEventListener('click', () => applyPatch('auto'));
  $('btn-custom')?.addEventListener('click', () => applyPatch('custom'));
  $('btn-clear-auto')?.addEventListener('click', () => clearLog('auto'));
  $('btn-clear-custom')?.addEventListener('click', () => clearLog('custom'));

  // Initial empty consoles
  clearLog('auto');
  clearLog('custom');
}

// ── Boot ─────────────────────────────────────────────────────

window.addEventListener('DOMContentLoaded', async () => {
  wire();
  bindProgressEvents();
  log('GitKraken Enhancer ready', 'ok', 'auto');
  await loadPatches();
  await scan();
});
