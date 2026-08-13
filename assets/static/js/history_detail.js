// 履歴詳細（/web/history/{jobID}）。生成対象の選択に合わせてカット一覧を絞ります。
//
// 削除とコピーは全ページ共通の data 属性（data-delete-url / data-copy-text）に載せているため、
// このページ固有のものはこの絞り込みだけです。
(() => {
    'use strict';

    const target = document.getElementById('gen-target');
    if (!target) return;

    target.addEventListener('change', () => {
        const selected = target.value;
        document.querySelectorAll('[data-section-group]').forEach((group) => {
            group.classList.toggle('d-none', selected !== 'full' && group.dataset.sectionGroup !== selected);
        });
    });
})();
