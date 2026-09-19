// Flow Music へ貼る本文の表示とコピー（履歴詳細）。
//
// 本文はページ描画時には埋め込まず、枠を初めて開いたときに GET /jobs/{jobID}/prompt から
// 取ります。詳細画面はカットを見比べるために開くことが多く、本文が要るのはその一部です。
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
            // 貼る先の上限は公開されていないので、長さだけ見えるようにしておきます。
            length.textContent = `${text.length.toLocaleString()} 文字`;
            copyButton.disabled = !text;
        } catch (error) {
            console.error('Flow prompt error:', error);
            errorBox.textContent = '本文を取得できませんでした。開き直すと再試行します。';
            errorBox.classList.remove('d-none');
            // 失敗したときだけ、次に開いたときにもう一度取りに行きます。
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
