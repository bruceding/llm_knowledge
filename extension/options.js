var wikiUrlInput = document.getElementById('wikiUrl');
var usernameInput = document.getElementById('username');
var passwordInput = document.getElementById('password');
var saveBtn = document.getElementById('saveBtn');
var statusDiv = document.getElementById('status');

// Load saved wiki URL on page open
chrome.storage.local.get(['wikiUrl', 'token'], function(config) {
  if (config.wikiUrl) {
    wikiUrlInput.value = config.wikiUrl;
  }
  if (config.token) {
    showStatus('success', '已连接');
  }
});

saveBtn.addEventListener('click', async function() {
  var wikiUrl = wikiUrlInput.value.trim().replace(/\/+$/, '');
  var username = usernameInput.value.trim();
  var password = passwordInput.value;

  // Validate
  if (!wikiUrl) {
    showStatus('error', '请输入 Wiki 地址');
    return;
  }
  if (!username) {
    showStatus('error', '请输入用户名');
    return;
  }
  if (!password) {
    showStatus('error', '请输入密码');
    return;
  }

  // Validate URL format
  try {
    new URL(wikiUrl);
  } catch (e) {
    showStatus('error', '请输入有效的 URL 地址');
    return;
  }

  saveBtn.disabled = true;
  saveBtn.textContent = '连接中...';
  statusDiv.className = 'status';
  statusDiv.style.display = 'none';

  try {
    // Login with extension client type (skips captcha)
    var loginRes = await fetch(wikiUrl + '/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: username,
        password: password,
        clientType: 'extension'
      })
    });

    var loginData = await loginRes.json();

    if (!loginRes.ok) {
      throw new Error(loginData.error || '登录失败');
    }

    // Save wiki URL and token (NOT password)
    await chrome.storage.local.set({
      wikiUrl: wikiUrl,
      token: loginData.token
    });

    // Clear password field
    passwordInput.value = '';

    showStatus('success', '连接成功');
  } catch (err) {
    showStatus('error', err.message || '连接失败');
  } finally {
    saveBtn.disabled = false;
    saveBtn.textContent = '保存并连接';
  }
});

function showStatus(type, message) {
  statusDiv.className = 'status ' + type;
  statusDiv.textContent = message;
  statusDiv.style.display = 'block';
}
