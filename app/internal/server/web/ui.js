const byId = id => document.getElementById(id);
const [launchToken, maintenanceToken] = location.hash.slice(1).split('.');
const token = launchToken || sessionStorage.getItem('quote0.token');
if (location.hash) {
  sessionStorage.setItem('quote0.token', token);
  history.replaceState(null, '', location.pathname);
}
const status = byId('status');
let submitting = false;

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
  try {
    const result = await api('device');
    byId('device').textContent = result.port ? '已识别 USB 串口：' + result.port : result.error;
  } catch (error) {
    byId('device').textContent = error.message;
  }
}

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
setInterval(refreshDevice, 3000);

let maintenanceInfo;
let maintenancePending = false;
const maintenanceButtons = [...document.querySelectorAll('[data-maintenance]')];

async function maintenanceRequest(path = '', method = 'GET', data, binary = false) {
  const response = await fetch('/v1/maintenance' + path, {
    method,
    headers: {
      'Authorization': 'Bearer ' + maintenanceToken,
      'Content-Type': binary ? 'application/octet-stream' : 'application/json',
    },
    body: data === undefined ? undefined : binary ? data : JSON.stringify(data),
  });
  if (!response.ok) throw new Error((await response.json()).error || response.status);
  return response;
}

function showMaintenance(info) {
  maintenanceInfo = info;
  const variant = byId('panel-variant').value;
  const active = info.busy || maintenancePending;
  const identified = info.available && !!info.mac && !!variant;
  const hasBackup = info.backups.includes(info.mac);
  byId('maintenance-controls').hidden = false;
  byId('maintenance-connection').textContent = info.mac
    ? '设备标识：' + info.mac + (info.port ? ' · ' + info.port : '')
    : '请只连接一台 Quote/0；未识别到带唯一 USB 标识的设备。';
  byId('panel-note').textContent = variant === 'early'
    ? '首发版暂不支持安装 Link 固件。仍可备份，或恢复同一台设备的原厂备份。'
    : '不能通过 USB 自动判断屏幕版本。不确定时请不要刷写。';
  if (!maintenancePending) {
    byId('maintenance-progress').textContent = info.message ||
      (info.available ? '' : '当前是开发包，缺少独立刷写工具。请使用完整安装包。');
  }
  byId('panel-variant').disabled = active;
  byId('manual-rom').disabled = active;
  for (const button of maintenanceButtons) {
    const action = button.dataset.maintenance;
    button.disabled = active || !identified ||
      (action === 'backup' && hasBackup) ||
      (action === 'restore' && !hasBackup) ||
      (action === 'install' && (variant !== 'standard' || !hasBackup));
  }
  const backupList = byId('backup-list');
  if ([...backupList.options].map(option => option.value).join(',') !== info.backups.join(',')) {
    const previous = backupList.value;
    backupList.replaceChildren(...info.backups.map(mac => new Option(mac, mac)));
    if (info.backups.includes(previous)) backupList.value = previous;
  }
  byId('backup-export').disabled = active || !info.backups.length;
  byId('backup-import-button').disabled = active;
}

async function refreshMaintenance() {
  if (!maintenanceToken || maintenancePending) return;
  try {
    showMaintenance(await (await maintenanceRequest()).json());
  } catch (error) {
    byId('maintenance-controls').hidden = true;
    byId('maintenance-connection').textContent = error.message;
  }
}

byId('panel-variant').onchange = () => {
  if (maintenanceInfo) showMaintenance(maintenanceInfo);
};
for (const button of maintenanceButtons) {
  button.onclick = async () => {
    if (!maintenanceInfo || maintenancePending) return;
    const action = button.dataset.maintenance;
    const variant = byId('panel-variant').value;
    const mac = maintenanceInfo.mac;
    const model = variant === 'standard' ? '标准版（UC8251D）' : '首发版（UC8151）';
    const warning = {
      backup: '请确认这台设备目前仍是原厂固件。App 会读取两次，不会写入 Flash。',
      install: '会替换设备固件。刷第三方固件可能影响保修；维护期间请勿拔线、休眠或退出。',
      restore: '会以备份中的整片 Flash 覆盖当前内容，丢弃备份之后的设备设置。维护期间请勿拔线。',
    }[action];
    if (!window.confirm(button.textContent + '\n设备 ' + mac + ' · ' + model + '\n\n' + warning)) return;
    maintenancePending = true;
    showMaintenance(maintenanceInfo);
    try {
      const request = {action, variant, mac, confirm: true, manual_rom: byId('manual-rom').checked};
      const result = await (await maintenanceRequest('', 'POST', request)).json();
      maintenancePending = false;
      showMaintenance(result);
    } catch (error) {
      maintenancePending = false;
      showMaintenance({...maintenanceInfo, message: error.message});
    }
  };
}
byId('backup-export').onclick = async () => {
  const mac = byId('backup-list').value;
  const result = byId('backup-result');
  try {
    const file = await (await maintenanceRequest('/backup/' + encodeURIComponent(mac))).blob();
    const url = URL.createObjectURL(file);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'quote0-' + mac + '.q0backup';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    result.textContent = '已发起下载；请把备份保存在私有位置。';
  } catch (error) {
    result.textContent = error.message;
  }
};
byId('backup-import-button').onclick = () => byId('backup-import').click();
byId('backup-import').onchange = async event => {
  const file = event.target.files[0];
  if (!file) return;
  const result = byId('backup-result');
  try {
    const info = await (await maintenanceRequest('/import', 'POST', file, true)).json();
    result.textContent = '已导入设备 ' + info.mac + ' 的备份。';
    await refreshMaintenance();
  } catch (error) {
    result.textContent = error.message;
  } finally {
    event.target.value = '';
  }
};
refreshMaintenance();
if (maintenanceToken) setInterval(refreshMaintenance, 1400);
