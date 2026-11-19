const byId = id => document.getElementById(id);
const [launchToken, maintenanceToken] = location.hash.slice(1).split('.');
const token = launchToken || sessionStorage.getItem('quote0.token');
if (location.hash) {
  sessionStorage.setItem('quote0.token', token);
  history.replaceState(null, '', location.pathname + location.search);
}
const status = byId('status');
let submitting = false;
let deviceRefreshing = false;

const navigation = [...document.querySelectorAll('[data-page]')];
const tabs = [...document.querySelectorAll('[data-tab]')];
let currentTab;

function showRoute() {
  const search = new URLSearchParams(location.search);
  const page = navigation.some(button => button.dataset.page === search.get('page')) ? search.get('page') : 'display';
  currentTab = tabs.some(button => button.dataset.tab === search.get('tab')) ? search.get('tab') : 'backup';
  for (const button of navigation) {
    const active = button.dataset.page === page;
    byId('page-' + button.dataset.page).hidden = !active;
    if (active) button.setAttribute('aria-current', 'page');
    else button.removeAttribute('aria-current');
  }
  for (const button of tabs) {
    const active = button.dataset.tab === currentTab;
    byId(button.getAttribute('aria-controls')).hidden = !active;
    button.setAttribute('aria-selected', String(active));
    button.tabIndex = active ? 0 : -1;
  }
  byId('variant-options').hidden = currentTab === 'restore';
  if (maintenanceInfo) showMaintenance(maintenanceInfo);
}

function navigate(page, tab = 'backup') {
  const url = new URL(location.href);
  url.searchParams.set('page', page);
  if (page === 'maintenance') url.searchParams.set('tab', tab);
  else url.searchParams.delete('tab');
  if (url.href !== location.href) history.pushState(null, '', url);
  showRoute();
}

for (const button of navigation) {
  button.onclick = () => navigate(button.dataset.page, currentTab);
}
for (const [index, button] of tabs.entries()) {
  button.onclick = () => navigate('maintenance', button.dataset.tab);
  button.onkeydown = event => {
    const offsets = {ArrowLeft: -1, ArrowRight: 1};
    let next;
    if (event.key in offsets) next = (index + offsets[event.key] + tabs.length) % tabs.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = tabs.length - 1;
    else return;
    event.preventDefault();
    tabs[next].click();
    tabs[next].focus();
  };
}
window.addEventListener('popstate', showRoute);

async function api(path, method = 'GET', data) {
  if (!token) throw new Error('请从菜单栏或托盘重新打开管理页。');
  const response = await fetch('/v1/' + path, {
    method,
    headers: {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'},
    body: data === undefined ? undefined : JSON.stringify(data),
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || response.status);
  return result;
}

async function refresh() {
  if (submitting) return;
  try {
    const result = await api('status');
    status.value = result.error || ({
      idle: '等待首次发送', queued: '已排队', sending: '刷新中…', shown: '设备已确认刷新',
    }[result.state] || result.state);
  } catch (error) {
    status.value = error.message;
  }
}

async function refreshDevice() {
  if (deviceRefreshing) return;
  deviceRefreshing = true;
  try {
    showDevice(await api('device'));
  } catch (error) {
    showDevice({state: 'unknown', error: error.message});
  } finally {
    deviceRefreshing = false;
  }
}

function showDevice(result) {
  const sidebar = byId('sidebar-device');
  if (result.state === 'busy' && sidebar.dataset.state === 'connected') return;
  sidebar.dataset.state = result.state;
  const labels = {
    connected: '设备已连接',
    disconnected: '设备未连接',
    unverified: 'USB 已识别，未确认固件',
    busy: '设备忙',
    maintenance: '设备维护中',
  };
  const connection = labels[result.state] || '设备状态不可用';
  let version = '固件版本 · —';
  let power = '电池电压 · 暂无读数';
  if (result.state === 'connected') {
    if (result.firmware_version) version = `固件 v${result.firmware_version}`;
    else if (result.legacy_firmware) version = '旧版固件（未报告版本）';
    else version = '固件版本未知';
    const source = result.usb_power === true ? 'USB 供电' : result.usb_power === false ? '未检测到 USB 供电' : '';
    const voltage = result.battery_mv ? `电池 ${(result.battery_mv / 1000).toFixed(2)} V` : '电池电压暂无读数';
    power = source ? source + ' · ' + voltage : voltage;
  }
  const detail = result.port ? connection + ' · ' + result.port : result.error || connection;
  const bundled = result.bundled_firmware_version;
  for (const [id, text] of [
    ['sidebar-connection', connection],
    ['sidebar-firmware', version],
    ['sidebar-power', power],
    ['sidebar-update', bundled ? `可升级到 v${bundled}` : ''],
    ['bundled-firmware', bundled ? `内置固件 v${bundled}` : ''],
    ['device', detail],
  ]) {
    const element = byId(id);
    if (element.textContent !== text) element.textContent = text;
  }
  byId('sidebar-update').hidden = !result.update_available || !bundled;
  byId('bundled-firmware').hidden = !bundled;
}

byId('sidebar-update').onclick = () => navigate('maintenance', 'install');

async function send(makeCard) {
  if (submitting) return;
  submitting = true;
  byId('test').disabled = true;
  byId('display').querySelector('[type=submit]').disabled = true;
  try {
    status.value = '准备发送…';
    await api('display', 'POST', await makeCard());
    submitting = false;
    await refresh();
  } catch (error) {
    status.value = error.message;
    await new Promise(resolve => setTimeout(resolve, 3000));
  } finally {
    submitting = false;
    byId('test').disabled = false;
    byId('display').querySelector('[type=submit]').disabled = false;
  }
}

byId('template').addEventListener('change', event => {
  byId('titles').hidden = event.target.value !== 'card';
  byId('image-field').hidden = event.target.value !== 'image';
  byId('body-field').hidden = event.target.value === 'image';
});
byId('test').addEventListener('click', () => send(() => ({template: 'test'})));
byId('display').addEventListener('submit', event => {
  event.preventDefault();
  send(async () => {
    const template = byId('template').value;
    if (template === 'image') {
      const file = byId('image').files[0];
      if (!file || file.size > 4 * 1024 * 1024) throw new Error('请选择不超过 4 MB 的 PNG 或 JPEG。');
      const encoded = await new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result.split(',')[1]);
        reader.onerror = reject;
        reader.readAsDataURL(file);
      });
      return {template, image_base64: encoded};
    }
    const body = byId('body').value;
    return template === 'card' ? {template, body, title: byId('title').value, subtitle: byId('subtitle').value} : {template, body};
  });
});

async function loadIntegrations() {
  const list = byId('integrations');
  try {
    for (const adapter of await api('integrations')) {
      const row = document.createElement('article');
      const name = document.createElement('h3');
      name.textContent = adapter.name;
      const note = document.createElement('p');
      note.textContent = adapter.note;
      const result = document.createElement('output');
      result.setAttribute('role', 'status');
      const actions = document.createElement('div');
      actions.className = 'actions';
      for (const enabled of [true, false]) {
        const button = document.createElement('button');
        button.textContent = enabled ? '启用 Hook' : '移除 Hook';
        button.className = enabled ? '' : 'secondary';
        button.onclick = async () => {
          for (const item of actions.children) item.disabled = true;
          try {
            const info = await api('integrations/' + adapter.id, 'PUT', {enabled});
            result.value = (info.enabled ? '已配置：' : '已移除：') + info.path;
          } catch (error) {
            result.value = error.message;
          } finally {
            for (const item of actions.children) item.disabled = false;
          }
        };
        actions.append(button);
      }
      row.append(name, note, actions, result);
      list.append(row);
    }
  } catch (error) {
    list.textContent = error.message;
  }
}

refresh();
refreshDevice();
loadIntegrations();
setInterval(refresh, 1200);
setInterval(refreshDevice, 4500);

let maintenanceInfo;
let maintenancePending = false;
let maintenancePolling = false;
let maintenanceError;
let maintenanceGeneration = 0;
let downloading = false;
const maintenanceButtons = [...document.querySelectorAll('[data-maintenance]')];
const restoreFile = byId('file-restore');

async function maintenanceRequest(path = '', method = 'GET', data) {
  const headers = {'Authorization': 'Bearer ' + maintenanceToken};
  if (data !== undefined && !(data instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
    data = JSON.stringify(data);
  }
  const response = await fetch('/v1/maintenance' + path, {
    method, headers, body: data,
  });
  if (!response.ok) throw new Error((await response.json()).error || response.status);
  return response;
}

function showMaintenance(info) {
  maintenanceInfo = info;
  const variant = byId('panel-variant').value;
  const active = info.busy || maintenancePending;
  const ready = info.available && !!info.mac && !active;
  byId('maintenance-controls').hidden = false;
  byId('maintenance-connection').textContent = info.mac
    ? '设备标识：' + info.mac + (info.port ? ' · ' + info.port : '')
    : '请只连接一台 Quote/0；未识别到带唯一 USB 标识的设备。';
  byId('panel-note').textContent = variant === 'early'
    ? '首发版可以备份和恢复，但暂不支持安装 Link 固件。'
    : '不能通过 USB 自动判断屏幕版本。不确定时请不要刷写。';
  const progress = byId('maintenance-progress');
  const resultVisible = info.busy || info.action === currentTab;
  const localError = maintenanceError?.action === currentTab ? maintenanceError.text : '';
  progress.textContent = maintenancePending ? '正在提交…' : localError || (resultVisible && info.message) ||
    (info.available ? '' : '当前是开发包，缺少独立刷写工具。请使用完整安装包。');
  progress.dataset.error = String(!maintenancePending && (!!localError || (resultVisible && info.state === 'error')));
  byId('panel-variant').disabled = active;
  byId('manual-rom').disabled = active;
  restoreFile.disabled = active;
  for (const button of maintenanceButtons) {
    const action = button.dataset.maintenance;
    button.disabled = !ready ||
      (action === 'backup' && !variant) ||
      (action === 'restore' && !restoreFile.files.length) ||
      (action === 'install' && variant !== 'standard');
  }
  byId('download-card').hidden = !info.download || (info.action === 'backup' && info.busy);
  byId('download-name').textContent = info.download?.filename || '';
  byId('backup-download').disabled = downloading;
}

async function refreshMaintenance() {
  if (!maintenanceToken || maintenancePending || maintenancePolling) return;
  maintenancePolling = true;
  const generation = maintenanceGeneration;
  try {
    const info = await (await maintenanceRequest()).json();
    if (generation === maintenanceGeneration) showMaintenance(info);
  } catch (error) {
    if (generation === maintenanceGeneration) {
      maintenanceInfo = undefined;
      byId('maintenance-controls').hidden = true;
      byId('maintenance-connection').textContent = error.message;
    }
  } finally {
    maintenancePolling = false;
  }
}

function updateMaintenanceOptions() {
  maintenanceError = undefined;
  if (maintenanceInfo) showMaintenance(maintenanceInfo);
}
byId('panel-variant').onchange = updateMaintenanceOptions;
restoreFile.onchange = updateMaintenanceOptions;

for (const button of maintenanceButtons) {
  button.onclick = async () => {
    if (!maintenanceInfo || maintenancePending || maintenanceInfo.busy) return;
    const action = button.dataset.maintenance;
    const variant = action === 'restore' ? '' : byId('panel-variant').value;
    const mac = maintenanceInfo.mac;
    const file = action === 'restore' ? restoreFile.files[0] : undefined;
    if (action === 'restore' && (!file || !/\.q0backup$/i.test(file.name) || file.size > 4 * 1024 * 1024 + 4096)) {
      maintenanceError = {action, text: '请选择有效的 .q0backup 文件。'};
      showMaintenance(maintenanceInfo);
      return;
    }
    const model = {standard: '标准版（UC8251D）', early: '首发版（UC8151）'}[variant];
    const warning = {
      backup: '请确认这台设备目前仍是原厂固件。App 会读取两次，不会写入 Flash。',
      install: '会替换设备固件。刷第三方固件可能影响保修；维护期间请勿拔线、休眠或退出。',
      restore: '会以备份中的整片 Flash 覆盖当前内容，丢弃备份之后的设备设置。维护期间请勿拔线。',
    }[action];
    const details = [button.textContent, '设备 ' + mac + (model ? ' · ' + model : ''), file ? '文件：' + file.name : '', '', warning];
    if (!window.confirm(details.join('\n'))) return;
    maintenanceGeneration++;
    maintenancePending = true;
    maintenanceError = undefined;
    showMaintenance(maintenanceInfo);
    try {
      const request = {action, variant, mac, confirm: true, manual_rom: byId('manual-rom').checked};
      let payload = request;
      if (file) {
        payload = new FormData();
        payload.append('request', JSON.stringify(request));
        payload.append('backup', file);
      }
      const result = await (await maintenanceRequest('', 'POST', payload)).json();
      if (file) restoreFile.value = '';
      if (action === 'backup') byId('download-result').textContent = '';
      maintenancePending = false;
      showMaintenance(result);
    } catch (error) {
      maintenancePending = false;
      maintenanceError = {action, text: error.message};
      showMaintenance(maintenanceInfo);
    }
  };
}
byId('backup-download').onclick = async () => {
  const download = maintenanceInfo?.download;
  if (!download || downloading) return;
  downloading = true;
  showMaintenance(maintenanceInfo);
  const result = byId('download-result');
  try {
    const file = await (await maintenanceRequest('/backup/' + encodeURIComponent(download.id))).blob();
    const url = URL.createObjectURL(file);
    const link = document.createElement('a');
    link.href = url;
    link.download = download.filename;
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    result.textContent = '已发起下载。请确认文件已保存，并留在私有位置。';
  } catch (error) {
    result.textContent = error.message;
  } finally {
    downloading = false;
    if (maintenanceInfo) showMaintenance(maintenanceInfo);
  }
};
showRoute();
refreshMaintenance();
if (maintenanceToken) setInterval(refreshMaintenance, 1400);
