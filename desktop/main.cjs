const { app, BrowserWindow, nativeImage } = require('electron');
const { spawn } = require('node:child_process');
const { existsSync } = require('node:fs');
const path = require('node:path');
const http = require('node:http');

const API_PORT = 18080;
let serverProcess;

function apiURL() {
  return process.env.IOTHUNTER_API_URL || `http://127.0.0.1:${API_PORT}`;
}

function bundledBinary() {
  const name = process.platform === 'win32' ? 'iothunter.exe' : 'iothunter';
  const candidates = [
    path.join(process.resourcesPath, name),
    path.join(process.resourcesPath, 'bin', name),
    path.join(__dirname, '..', 'bin', name),
  ];
  return candidates.find((candidate) => existsSync(candidate));
}

function startSidecar() {
  if (process.env.IOTHUNTER_API_URL) return;
  const binary = bundledBinary();
  if (!binary) return;
  const dataDir = path.join(app.getPath('userData'), 'state.json');
  serverProcess = spawn(binary, ['serve', '--addr', `127.0.0.1:${API_PORT}`, '--data', dataDir], {
    stdio: 'ignore',
    windowsHide: true,
  });
  serverProcess.on('error', (error) => console.error('IoTHunter sidecar:', error.message));
}

function waitForHealth(attempts = 50) {
  return new Promise((resolve) => {
    const check = () => {
      const request = http.get(`${apiURL()}/healthz`, (response) => {
        response.resume();
        if (response.statusCode === 200) return resolve();
        retry();
      });
      request.on('error', retry);
      request.setTimeout(250, () => request.destroy());
    };
    const retry = () => {
      if (--attempts <= 0) return resolve();
      setTimeout(check, 100);
    };
    check();
  });
}

async function createWindow() {
  startSidecar();
  await waitForHealth();
  const iconCandidates = [path.join(__dirname, '..', 'logo2.png'), path.join(process.resourcesPath, 'logo2.png')];
  const iconPath = iconCandidates.find((candidate) => existsSync(candidate));
  const icon = iconPath ? nativeImage.createFromPath(iconPath) : undefined;
  const window = new BrowserWindow({
    width: 1380,
    height: 860,
    minWidth: 760,
    minHeight: 560,
    backgroundColor: '#f7f9fc',
    icon,
    title: 'IoTHunter',
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  window.loadFile(path.join(__dirname, 'renderer', 'index.html'));
  return window;
}

app.whenReady().then(async () => {
  await createWindow();
  app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createWindow(); });
});

app.on('window-all-closed', () => {
  if (serverProcess && !serverProcess.killed) serverProcess.kill();
  if (process.platform !== 'darwin') app.quit();
});
