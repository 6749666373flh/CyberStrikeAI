import { test, expect, ready, UI_PERMISSIONS } from './fixtures.mjs';

const auditURL = 'https://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1';
const mainModel = { provider: 'claude', base_url: 'https://main.example.test', model: 'main-model', api_key: '********' };

async function openAuditCard(main) {
    const section = main.locator('.settings-audit-model');
    await section.getByText('审计模型', { exact: true }).click();
    return section.locator('.settings-probe-card');
}

test.describe('audit model configuration and connection test', () => {
    test.use({ appPermissions: [...UI_PERMISSIONS, 'config:write'] });

    for (const entry of [
        { name: 'Qwen alongside a Claude main model', backend: 'openai', provider: 'openai_compatible', url: auditURL, model: 'qwen4-max', protocol: '兼容 OpenAI 的接口' },
        { name: 'TypeSafe Jev', backend: 'typesafe', url: '', model: '', protocol: 'TypeSafe / Jev' },
        { name: 'Jev backend alias', backend: ' Jev ', url: '', model: '', protocol: 'TypeSafe / Jev' },
        { name: 'default chat backend inherits the main model', url: '', model: '', protocol: 'Claude (Anthropic Messages)' }
    ]) {
        test(entry.name, async ({ page, mockApi }) => {
            mockApi.respond('GET', '/api/config', { json: {
                openai: mainModel, tools: [],
                hitl: { audit_backend: entry.backend, audit_model: {
                    provider: entry.provider, base_url: entry.url, model: entry.model, api_key: '********'
                } }
            } });
            mockApi.respond('POST', '/api/config/test-audit-model', { json: { success: true, model: entry.model || 'configured-model', latency_ms: 123 } });
            await page.goto('/static/app/#/settings?section=models');
            const main = await ready(page);
            const card = await openAuditCard(main);
            const save = card.getByRole('button', { name: '保存审计模型', exact: true });
            await expect(save).toBeDisabled();
            await expect(card.getByText(entry.protocol, { exact: true })).toBeVisible();
            await expect(card.getByLabel('API 地址', { exact: true })).toHaveValue(entry.url);
            await expect(card.getByLabel('API 密钥', { exact: true })).toHaveAttribute('type', 'password');
            if (!entry.url) {
                await expect(card.getByLabel('API 地址', { exact: true })).toHaveAttribute('placeholder', entry.protocol === 'TypeSafe / Jev' ? 'https://api.typesafe.ai' : mainModel.base_url);
            }
            await card.getByRole('button', { name: '测试审计模型', exact: true }).click();
            await expect(card.getByText('连接成功', { exact: true })).toBeVisible();
            expect(mockApi.writes().map(({ path, body }) => [path, body])).toEqual([
                ['/api/config/test-audit-model', { base_url: entry.url, api_key: '********', model: entry.model }]
            ]);

            // Form edits can be tested before saving without mutating server config until Save is clicked.
            await card.getByLabel('模型', { exact: true }).fill('draft-model');
            await expect(save).toBeEnabled();
            await expect(card.getByText('连接成功', { exact: true })).toHaveCount(0);
            mockApi.respond('POST', '/api/config/test-audit-model', { json: { success: false, error: 'API 返回错误 (HTTP 401): invalid credential', status_code: 401 } });
            await card.getByRole('button', { name: '测试审计模型', exact: true }).click();
            await expect(card.getByText('API 返回错误 (HTTP 401): invalid credential', { exact: true })).toBeVisible();
            expect(mockApi.writes()).toHaveLength(2);
            expect(mockApi.writes()[1]).toMatchObject({ path: '/api/config/test-audit-model', body: { model: 'draft-model' } });
        });
    }

    test('saves audit model edits, supports reset, and retries apply on failure', async ({ page, mockApi }) => {
        const config = {
            openai: mainModel,
            tools: [],
            hitl: { audit_backend: 'openai', audit_model: { provider: '', base_url: '', model: '', api_key: '' } }
        };
        mockApi.respond('GET', '/api/config', () => ({ json: config }));
        mockApi.fail('PUT', '/api/config', '示例审计模型保存失败');

        await page.goto('/static/app/#/settings?section=models');
        const main = await ready(page);
        const card = await openAuditCard(main);
        const save = card.getByRole('button', { name: '保存审计模型', exact: true });
        await expect(save).toBeDisabled();

        // Fill fields and verify reset restores baseline.
        await card.getByLabel('API 地址', { exact: true }).fill('https://api.deepseek.com/v1');
        await card.getByLabel('API 密钥', { exact: true }).fill('sk-audit');
        await card.getByLabel('模型', { exact: true }).fill('deepseek-flash');
        const provider = card.getByRole('combobox', { name: 'API 提供商', exact: true });
        await provider.click();
        await page.getByRole('option').filter({ hasText: '兼容 OpenAI 的接口' }).click();
        await expect(save).toBeEnabled();

        await card.getByRole('button', { name: '撤销修改', exact: true }).click();
        await expect(save).toBeDisabled();
        await expect(card.getByLabel('模型', { exact: true })).toHaveValue('');

        // Re-apply changes, hit save failure, then succeed with apply retry.
        await card.getByLabel('API 地址', { exact: true }).fill('https://api.deepseek.com/v1');
        await card.getByLabel('API 密钥', { exact: true }).fill('sk-audit');
        await card.getByLabel('模型', { exact: true }).fill('deepseek-flash');
        await provider.click();
        await page.getByRole('option').filter({ hasText: '兼容 OpenAI 的接口' }).click();

        await save.click();
        await expect(card.getByText('示例审计模型保存失败', { exact: true })).toBeVisible();
        await expect(save).toBeEnabled();

        mockApi.respond('PUT', '/api/config', ({ body }) => {
            const patch = body.hitl_audit_model;
            if (patch.audit_backend !== undefined) config.hitl.audit_backend = patch.audit_backend;
            Object.assign(config.hitl.audit_model, {
                ...(patch.provider !== undefined ? { provider: patch.provider } : {}),
                ...(patch.base_url !== undefined ? { base_url: patch.base_url } : {}),
                ...(patch.api_key !== undefined ? { api_key: patch.api_key ? '********' : '' } : {}),
                ...(patch.model !== undefined ? { model: patch.model } : {})
            });
            return { json: {} };
        });
        mockApi.fail('POST', '/api/config/apply', '示例审计配置应用失败');

        await save.click();
        await expect(save).toBeDisabled();
        expect(mockApi.calls('PUT', '/api/config').at(-1).body).toEqual({
            hitl_audit_model: {
                provider: 'openai_compatible',
                base_url: 'https://api.deepseek.com/v1',
                api_key: 'sk-audit',
                model: 'deepseek-flash'
            }
        });
        const retry = card.getByRole('button', { name: '重试应用', exact: true });
        await expect(retry).toBeEnabled();
        mockApi.respond('POST', '/api/config/apply', { json: {} });
        await retry.click();
        await expect(retry).toHaveCount(0);
        await expect(main.locator('.settings-audit-model').getByText('deepseek-flash', { exact: true })).toBeVisible();
    });
});

test('audit model viewer cannot edit, save, or start a test', async ({ page, mockApi }) => {
    mockApi.respond('GET', '/api/config', { json: { openai: mainModel, tools: [] } });
    await page.goto('/static/app/#/settings?section=models');
    const main = await ready(page);
    const card = await openAuditCard(main);
    await expect(card.getByLabel('API 地址', { exact: true })).toHaveAttribute('readonly', '');
    await expect(card.getByRole('button', { name: '保存审计模型', exact: true })).toHaveCount(0);
    await expect(card.getByRole('button', { name: '测试审计模型', exact: true })).toBeDisabled();
    expect(mockApi.writes()).toEqual([]);
});

test('chat HITL drawer reviewer segmented control keeps compact spacing', async ({ page, mockApi }) => {
    mockApi.respond('GET', '/api/hitl/default-config', { json: {
        defaultMode: 'approval', defaultReviewer: 'audit_agent', defaultTimeoutSeconds: 300, hitlGlobalToolWhitelist: ['tool_search']
    } });
    await page.goto('/static/app/#/chat');
    const main = await ready(page);
    await main.locator('.chat-composer-controls').getByRole('button', { name: '人机协同', exact: true }).click();
    const dialog = page.getByRole('dialog').filter({ hasText: '人机协同' });
    const group = dialog.locator('.chat-setting-reviewer');
    const options = group.locator('.semi-radio');
    await expect(options).toHaveCount(2);
    const [firstBox, secondBox, groupBox] = await Promise.all([
        options.nth(0).boundingBox(),
        options.nth(1).boundingBox(),
        group.boundingBox()
    ]);
    expect(secondBox.x - (firstBox.x + firstBox.width)).toBeLessThanOrEqual(12);
    expect(groupBox.width).toBeLessThan(260);
});
