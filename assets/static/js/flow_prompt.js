// Showing and copying the text to paste into Flow Music (history detail).
//
// The text is not embedded at page render; it is fetched from GET /jobs/{jobID}/prompt the first
// time the panel opens. The detail page is mostly opened to compare cuts, and only some visits need the text.
(() => {
    'use strict';

    const panel = document.querySelector('.flow-prompt-panel');
    if (!panel) return;

    const output = panel.querySelector('.flow-prompt-text');
    const length = panel.querySelector('.flow-prompt-length');
    const copyButton = panel.querySelector('.flow-prompt-copy-btn');
    const errorBox = panel.querySelector('.flow-prompt-error');

    async function load() {
        errorBox.classList.add('d-none');
        try {
            const response = await fetch(panel.dataset.promptUrl, {headers: {'Accept': 'application/json'}});
            if (!response.ok) {
                throw new Error(`HTTP ${response.status}`);
            }
            const text = (await response.json()).prompt || '';
            output.textContent = text;
            // The paste target's limit is not published, so at least make the length visible.
            length.textContent = `${text.length.toLocaleString()} 文字`;
            copyButton.disabled = !text;
        } catch (error) {
            console.error('Flow prompt error:', error);
            errorBox.textContent = '本文を取得できませんでした。開き直すと再試行します。';
            errorBox.classList.remove('d-none');
            // Only on failure, fetch again the next time it opens.
            panel.addEventListener('toggle', onToggle);
        }
    }

    function onToggle() {
        if (!panel.open) return;
        panel.removeEventListener('toggle', onToggle);
        load();
    }

    panel.addEventListener('toggle', onToggle);
    copyButton.addEventListener('click', () => {
        window.App.copyToClipboard(output.textContent || '', copyButton);
    });
})();
