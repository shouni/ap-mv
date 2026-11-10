// Utilities shared by every page. layout.html loads this first with defer
// (defer keeps document order), so what is defined here can be used synchronously
// from the page-specific scripts.
window.App = window.App || {};

(() => {
    'use strict';

    const App = window.App;

    App.csrfToken = () => document.getElementById('csrf_token')?.value || '';

    // --- Delete ---------------------------------------------------------------

    // While a DELETE to the same URL is in flight, do not send another. This keeps rapid clicks
    // right after closing the confirm dialog from delivering DELETE twice to the same job.
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
            // Always reload on success so no row removed from the listing is left showing.
            // On pages whose target itself is gone, such as the detail page, go back to the listing.
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

    // Delete buttons are declared with data attributes only. They used to have
    // onclick="deleteDraft('{{.JobID}}', '{{.CSRFToken}}')", embedding
    // template values as JS arguments.
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

    // --- Confirm before submitting -------------------------------------------

    // Irreversible submissions (Veo re-renders and the like) are declared with data-confirm.
    // Unlike onsubmit="return confirm('...')", template values in the message never end up
    // inside a JS string literal.
    document.addEventListener('submit', (event) => {
        const message = event.target.dataset?.confirm;
        if (message && !confirm(message)) {
            event.preventDefault();
        }
    });

    // --- Clipboard ------------------------------------------------------------

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

    // --- Navigation -----------------------------------------------------------

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
