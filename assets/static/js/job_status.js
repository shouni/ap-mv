// The accepted page (queued.html). Polls /jobs/{jobID} and reflects
// queued → running → succeeded/failed.
//
// The server-side record (statusRecorder) had been working all along; this page was the only unconnected piece.
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
            // Regeneration commands write their artifacts on the original job's side, so point to that job's history.
            const jobForHistory = status.original_job_id || jobID;
            historyLink.href = `/jobs/${encodeURIComponent(jobForHistory)}`;
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
            // 404 also happens for "jobs from before this feature" and the moment before recording, so carry on quietly.
            if (!resp.ok) return;
            render(await resp.json());
        } catch (error) {
            // Leave transient failures to the next poll
        }
    }

    poll();
    timer = setInterval(poll, POLL_INTERVAL_MS);
})();
