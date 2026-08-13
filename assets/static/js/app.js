// 全ページ共通のユーティリティ。layout.html が defer で最初に読み込むため
// （defer は記述順を保つ）、ここで定義したものはページ個別のスクリプトから
// 同期的に使えます。
window.App = window.App || {};

(() => {
    'use strict';

    const App = window.App;

    App.csrfToken = () => document.getElementById('csrf_token')?.value || '';

    // --- 削除 ---------------------------------------------------------------

    // 同じ URL への DELETE が飛んでいる間は次を投げません。確認ダイアログを
    // 閉じた直後の連打で、同じジョブに DELETE が二重に届くのを防ぎます。
    const inFlightDeletes = new Set();

    App.deleteResource = async ({url, confirmMessage, redirectTo}) => {
        if (inFlightDeletes.has(url) || !confirm(confirmMessage)) {
            return false;
        }

        inFlightDeletes.add(url);
        try {
            const resp = await fetch(url, {
                method: 'DELETE',
                headers: {'X-CSRF-Token': App.csrfToken()}
            });
            if (!resp.ok) {
                alert(`Delete failed: ${resp.statusText}`);
                return false;
            }
            // 一覧から消えた行を残さないため、成功時は必ず読み直します。
            // 詳細ページのように対象そのものが無くなる画面では一覧へ戻します。
            if (redirectTo) {
                window.location.href = redirectTo;
            } else {
                window.location.reload();
            }
            return true;
        } catch (error) {
            console.error('Delete Error:', error);
            alert('通信エラーが発生しました。');
            return false;
        } finally {
            inFlightDeletes.delete(url);
        }
    };

    // 削除ボタンは data 属性だけで宣言します。以前は onclick に
    // deleteDraft('{{.JobID}}', '{{.CSRFToken}}') と書いており、
    // テンプレートの値が JS の引数として埋め込まれていました。
    document.addEventListener('click', (event) => {
        const button = event.target.closest('[data-delete-url]');
        if (!button) return;

        event.preventDefault();
        App.deleteResource({
            url: button.dataset.deleteUrl,
            confirmMessage: button.dataset.deleteConfirm || '削除しますか？',
            redirectTo: button.dataset.deleteRedirect || ''
        });
    });

    // --- 送信前の確認 -------------------------------------------------------

    // 取り消せない送信（Veo の焼き直しなど）は data-confirm で宣言します。
    // onsubmit="return confirm('...')" と違い、文言に含まれるテンプレート値が
    // JS の文字列リテラルへ入らずに済みます。
    document.addEventListener('submit', (event) => {
        const message = event.target.dataset?.confirm;
        if (message && !confirm(message)) {
            event.preventDefault();
        }
    });

    // --- クリップボード -----------------------------------------------------

    const COPY_RESET_MS = 1500;

    App.copyToClipboard = async (text, button) => {
        if (!text) return false;

        try {
            await navigator.clipboard.writeText(text);
        } catch (error) {
            console.error('Failed to copy to clipboard:', error);
            alert(`Copy failed. Please copy manually:\n${text}`);
            return false;
        }

        if (button) {
            const original = button.textContent;
            button.textContent = 'Copied!';
            setTimeout(() => { button.textContent = original; }, COPY_RESET_MS);
        }
        return true;
    };

    document.addEventListener('click', (event) => {
        const button = event.target.closest('[data-copy-text]');
        if (!button) return;
        App.copyToClipboard(button.dataset.copyText, button);
    });

    // --- ナビゲーション -----------------------------------------------------

    document.addEventListener('DOMContentLoaded', () => {
        const currentPath = window.location.pathname;
        document.querySelectorAll('.navbar-nav .nav-link').forEach((link) => {
            const href = link.getAttribute('href');
            if (href === currentPath || (href !== '/' && currentPath.startsWith(href))) {
                link.classList.add('active');
            }
        });
    });
})();
