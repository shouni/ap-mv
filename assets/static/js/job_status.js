// 受付画面（queued.html）。/jobs/{jobID} をポーリングして
// queued → running → succeeded/failed を反映します。
//
// サーバー側の記録（statusRecorder）は以前から動いており、この画面が唯一の未接続点でした。
(() => {
    'use strict';

    const panel = document.getElementById('job-status-panel');
    if (!panel) return;

    const jobID = panel.dataset.jobId;
    if (!jobID) return;

    const POLL_INTERVAL_MS = 5000;

    const STATE_BADGES = {
        queued: 'text-bg-secondary',
        running: 'text-bg-info',
        succeeded: 'text-bg-success',
        failed: 'text-bg-danger'
    };

    const stateEl = document.getElementById('job-status-state');
    const iconEl = document.getElementById('job-status-icon');
    const spinnerEl = document.getElementById('job-status-spinner');
    const errorEl = document.getElementById('job-status-error');
    const historyLink = document.getElementById('job-status-history-link');

    let timer = null;

    function stop() {
        if (timer !== null) {
            clearInterval(timer);
            timer = null;
        }
    }

    function render(status) {
        const state = status.state || 'queued';
        stateEl.textContent = state;
        stateEl.className = `badge ${STATE_BADGES[state] || STATE_BADGES.queued}`;

        if (state === 'succeeded') {
            iconEl.className = 'bi bi-check-circle-fill text-success fs-2';
            spinnerEl.classList.add('d-none');
            // 再生成系は成果物が元ジョブ側に書かれるため、そちらの履歴へ案内する。
            const jobForHistory = status.original_job_id || jobID;
            historyLink.href = `/history/${encodeURIComponent(jobForHistory)}`;
            historyLink.classList.remove('d-none');
            stop();
            return;
        }

        if (state === 'failed') {
            iconEl.className = 'bi bi-x-circle-fill text-danger fs-2';
            spinnerEl.classList.add('d-none');
            if (status.error) {
                errorEl.textContent = status.error;
                errorEl.classList.remove('d-none');
            }
            stop();
        }
    }

    async function poll() {
        try {
            const resp = await fetch(`/jobs/${encodeURIComponent(jobID)}`, {
                headers: {Accept: 'application/json'}
            });
            // 404 は「この機能より前のジョブ」や記録前の一瞬でも起こるため、静かに続行。
            if (!resp.ok) return;
            render(await resp.json());
        } catch (error) {
            // 一時的な失敗は次のポーリングに任せる
        }
    }

    poll();
    timer = setInterval(poll, POLL_INTERVAL_MS);
})();
