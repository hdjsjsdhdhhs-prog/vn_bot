// No frontend toolchain or dependencies: node --test internal/web/testdata/connect_copy.test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const template = fs.readFileSync(path.join(__dirname, '../templates/connect.html'), 'utf8');
const script = template.match(/<script>([\s\S]*?)<\/script>/)[1];
const url = 'https://customer.example/sub/' + 'a'.repeat(64);

function setup(clipboard, secure = true, fallback = () => true) {
    let click;
    let selected = false;
    let selection;
    const input = { value: url, focus() {}, select() { selected = true; }, setSelectionRange(start, end) { selection = [start, end]; } };
    const status = { textContent: '' };
    const button = { addEventListener(event, handler) { assert.equal(event, 'click'); click = handler; } };
    let fallbacks = 0;
    vm.runInNewContext(script, {
        document: {
            getElementById(id) { return { 'subscription-url': input, 'copy-url': button, 'copy-status': status }[id]; },
            execCommand(command) { assert.equal(command, 'copy'); fallbacks++; return fallback(); }
        },
        navigator: { clipboard }, window: { isSecureContext: secure }
        // No network or storage APIs are available in the copy action.
    });
    return {
        async click() { click(); await Promise.resolve(); },
        status,
        assertSelected() { assert.equal(selected, true); assert.deepEqual(selection, [0, url.length]); },
        fallbacks() { return fallbacks; }
    };
}

test('Clipboard API copies exactly the input URL', async () => {
    let copied;
    const page = setup({ writeText(value) { copied = value; return Promise.resolve(); } });
    await page.click();
    assert.equal(copied, url);
    assert.equal(page.status.textContent, 'Ссылка скопирована.');
    assert.equal(page.fallbacks(), 0);
});

for (const [name, clipboard, secure] of [
    ['missing API', undefined, true],
    ['partial API', {}, true],
    ['insecure context', { writeText() { throw new Error('must not be called'); } }, false],
    ['denied permission', { writeText() { return Promise.reject(new Error('denied')); } }, true],
    ['WebView synchronous error', { writeText() { throw new Error('blocked'); } }, true]
]) {
    test(name + ' falls back to local selection/copy', async () => {
        const page = setup(clipboard, secure);
        await page.click();
        assert.equal(page.fallbacks(), 1);
        page.assertSelected();
        assert.equal(page.status.textContent, 'Ссылка скопирована.');
    });
}

for (const [name, fallback] of [
    ['returns false', () => false],
    ['throws', () => { throw new Error('blocked'); }]
]) {
    test('failed fallback ' + name + ' offers manual copy without false success', async () => {
        const page = setup(undefined, false, fallback);
        await page.click();
        page.assertSelected();
        assert.equal(page.status.textContent, 'Выделите и скопируйте ссылку вручную.');
    });
}
