import './style.css';
import { DetectAsar, GetPatches, SelectAsar, ApplyPatch } from '../wailsjs/go/main/App';

let patches = [];
let autoAsar = null, autoPatch = null;
let customAsar = null, customPatch = null;

const $ = id => document.getElementById(id);

function log(msg, type = '', el) {
  if (!el) el = $('log-auto');
  el.innerHTML += `<span class="${type}">${msg}</span>\n`;
  el.scrollTop = el.scrollHeight;
}

// Tabs
document.querySelectorAll('.tab').forEach(tab => {
  tab.onclick = () => {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    tab.classList.add('active');
    document.querySelectorAll('.tab-content').forEach(c => c.style.display = 'none');
    document.getElementById('tab-' + tab.dataset.tab).style.display = 'flex';
  };
});

async function loadPatches() {
  patches = await GetPatches();
  const list = $('patchList');
  list.innerHTML = '';
  if (!patches.length) { list.innerHTML = '<span class="loading">No patches found</span>'; return; }
  patches.forEach(p => {
    const div = document.createElement('div');
    div.className = 'item';
    div.innerHTML = `<span class="v">v${p.version}</span> <span class="meta">${p.patches.length} edits</span>`;
    div.onclick = () => {
      document.querySelectorAll('#patchList .item').forEach(e => e.classList.remove('selected'));
      div.classList.add('selected');
      customPatch = p;
      log(`Selected v${p.version}`, 'info', $('log-custom'));
      updateCustomBtn();
    };
    list.appendChild(div);
  });
}

function updateAutoBtn() { $('btn-auto').disabled = !(autoAsar && autoPatch); }
function updateCustomBtn() { $('btn-custom').disabled = !(customAsar && customPatch); }

window.scan = async function scan() {
  const c = $('detectedList');
  c.innerHTML = '<div class="loading">Scanning...</div>';
  const dir = await DetectAsar();
  c.innerHTML = '';
  if (!dir.length) { c.innerHTML = '<span class="loading">No GitKraken found</span>'; return; }
  let found = false;
  for (const item of dir) {
    const div = document.createElement('div');
    div.className = 'item';
    const sz = item.size;
    const mb = (sz / 1024 / 1024).toFixed(1);
    const gb = (sz / 1024 / 1024 / 1024).toFixed(1);
    const sizeStr = sz >= 1073741824 ? gb + ' GB' : mb + ' MB';
    const patch = patches.find(p => p.version === item.version);
    div.innerHTML = `
      <div><span class="v">v${item.version}</span> <span class="meta">${sizeStr}</span></div>
      ${patch ? '<span class="tag tag-ok">Patch ready</span>' : '<span class="tag tag-wait">No patch yet</span>'}
    `;
    if (patch) {
      found = true;
      div.onclick = () => {
        document.querySelectorAll('#detectedList .item').forEach(e => e.classList.remove('selected'));
        div.classList.add('selected');
        autoAsar = item;
        autoPatch = patch;
        log(`Selected v${item.version} for patching`, 'ok');
        updateAutoBtn();
      };
      if (!autoAsar) { autoAsar = item; autoPatch = patch; div.classList.add('selected'); }
    } else {
      div.onclick = () => log(`No patch for v${item.version} yet, wait for update`, 'err');
    }
    c.appendChild(div);
  }
  if (!found) { $('btn-auto').disabled = true; log('No compatible patch found', 'err'); }
  else log('Click a version then Apply', 'info');
};

window.browse = async function browse() {
  const path = await window.runtime.OpenFileDialog({ filters: [{ DisplayName: 'asar', Pattern: '*.asar' }] });
  if (!path) return;
  const r = await SelectAsar(path);
  if (!r || !r.path) return;
  const patch = patches.find(p => p.version === r.version);
  autoAsar = r; autoPatch = patch || null;
  log(`Loaded v${r.version}${patch ? ' - patch found' : ' - no patch'}`, patch ? 'ok' : 'err');
  updateAutoBtn();
};

window.applyAuto = async function applyAuto() {
  if (!autoAsar || !autoPatch) return;
  const btn = $('btn-auto'); btn.disabled = true; btn.textContent = 'Applying...';
  const r = await ApplyPatch(autoAsar.path, autoPatch);
  log(r.success ? 'OK ' + r.message : 'FAIL ' + r.message, r.success ? 'ok' : 'err');
  btn.disabled = false; btn.textContent = 'Apply Patch'; updateAutoBtn();
};

window.browseCustom = async function browseCustom() {
  const path = await window.runtime.OpenFileDialog({ filters: [{ DisplayName: 'asar', Pattern: '*.asar' }] });
  if (!path) return;
  const r = await SelectAsar(path);
  if (!r || !r.path) return;
  customAsar = r;
  $('asarInfo').textContent = `v${r.version || '?'} - ${r.path}`;
  log(`Loaded v${r.version || '?'}`, 'info', $('log-custom'));
  updateCustomBtn();
};

window.detectCustom = async function detectCustom() {
  const list = await DetectAsar();
  const c = $('detectedCustom'); c.innerHTML = '';
  if (!list.length) { log('No installations found', 'err', $('log-custom')); return; }
  list.forEach(item => {
    const div = document.createElement('div');
    div.className = 'item';
    const sz = item.size;
    const sizeStr = sz >= 1073741824 ? (sz / 1024 / 1024 / 1024).toFixed(1) + ' GB' : (sz / 1024 / 1024).toFixed(1) + ' MB';
    div.innerHTML = `<span class="v">v${item.version}</span> <span class="meta">${sizeStr}</span>`;
    div.onclick = () => {
      document.querySelectorAll('#detectedCustom .item').forEach(e => e.classList.remove('selected'));
      div.classList.add('selected');
      customAsar = item;
      $('asarInfo').textContent = `v${item.version} - ${item.path}`;
      log(`Loaded v${item.version}`, 'info', $('log-custom'));
      updateCustomBtn();
    };
    c.appendChild(div);
  });
};

window.applyCustom = async function applyCustom() {
  if (!customAsar || !customPatch) return;
  const btn = $('btn-custom'); btn.disabled = true; btn.textContent = 'Applying...';
  const r = await ApplyPatch(customAsar.path, customPatch);
  log(r.success ? 'OK ' + r.message : 'FAIL ' + r.message, r.success ? 'ok' : 'err', $('log-custom'));
  btn.disabled = false; btn.textContent = 'Apply Patch'; updateCustomBtn();
};

window.addEventListener('DOMContentLoaded', () => {
  loadPatches().then(() => scan());
});
