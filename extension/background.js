// Handle toolbar icon click
chrome.action.onClicked.addListener(async (tab) => {
  const config = await chrome.storage.local.get(['wikiUrl', 'token']);

  if (!config.wikiUrl || !config.token) {
    chrome.runtime.openOptionsPage();
    return;
  }

  // Set badge to show clipping in progress
  chrome.action.setBadgeText({ text: '...', tabId: tab.id });
  chrome.action.setBadgeBackgroundColor({ color: '#888888', tabId: tab.id });

  try {
    // Inject content script and extract page content
    await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      files: ['content.js']
    });

    // Send extract message to content script
    const response = await chrome.tabs.sendMessage(tab.id, { action: 'extractContent' });

    if (!response || !response.html) {
      throw new Error('Failed to extract page content');
    }

    // Send to wiki API
    const apiResponse = await fetch(`${config.wikiUrl}/api/raw/web-clip`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${config.token}`
      },
      body: JSON.stringify({
        url: response.url,
        title: response.title,
        html: response.html
      })
    });

    if (apiResponse.status === 401) {
      await chrome.storage.local.remove('token');
      chrome.action.setBadgeText({ text: '!', tabId: tab.id });
      chrome.action.setBadgeBackgroundColor({ color: '#EF4444', tabId: tab.id });
      chrome.tabs.sendMessage(tab.id, {
        action: 'showToast',
        success: false,
        message: '登录已过期，请重新设置'
      });
      setTimeout(() => chrome.action.setBadgeText({ text: '', tabId: tab.id }), 3000);
      return;
    }

    const data = await apiResponse.json();

    if (!apiResponse.ok) {
      throw new Error(data.error || 'Clip failed');
    }

    // Success
    chrome.action.setBadgeText({ text: '✓', tabId: tab.id });
    chrome.action.setBadgeBackgroundColor({ color: '#22C55E', tabId: tab.id });
    chrome.tabs.sendMessage(tab.id, {
      action: 'showToast',
      success: true,
      message: '已收藏'
    });
  } catch (err) {
    chrome.action.setBadgeText({ text: '✗', tabId: tab.id });
    chrome.action.setBadgeBackgroundColor({ color: '#EF4444', tabId: tab.id });
    chrome.tabs.sendMessage(tab.id, {
      action: 'showToast',
      success: false,
      message: err.message || '收藏失败'
    });
  }

  // Clear badge after 3 seconds
  setTimeout(() => {
    chrome.action.setBadgeText({ text: '', tabId: tab.id });
  }, 3000);
});
