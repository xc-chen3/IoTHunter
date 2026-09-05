const { contextBridge } = require('electron');

contextBridge.exposeInMainWorld('iothunter', {
  apiBase: process.env.IOTHUNTER_API_URL || 'http://127.0.0.1:18080',
  platform: process.platform,
});
