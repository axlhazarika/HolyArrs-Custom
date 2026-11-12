<!-- Usenet servers (pushed to SABnzbd) and debrid accounts (written into Decypharr), kept in one place. Owner only. -->
<script lang="ts">
	import { t } from '#lib/core/i18n/i18n.svelte.ts';
	import { SkSettings } from '#lib/ui/sk/index.ts';
	import PageHeader from '#lib/ui/PageHeader.svelte';
	import SettingsCard from '#lib/ui/SettingsCard.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Field from '#lib/ui/Field.svelte';
	import LoadError from '#lib/ui/LoadError.svelte';
	import { api, ApiError } from '#lib/core/api.ts';
	import { plainMessage as message } from '#lib/core/settings.ts';
	import { toasts } from '#lib/core/toasts.svelte.ts';
	import { ago } from '#lib/core/format.ts';
	import { onMount } from 'svelte';
	import { MASKED, blankForm, defaultDebridName, formOf, inputOf, stateWord, type Entry, type Form, type Kind, type Listing } from './providers.ts';

	let loaded = $state(false);
	let error = $state('');
	let list = $state<Listing | null>(null);
	let form = $state<Form | null>(null);
	let saving = $state(false);
	let busy = $state<Record<string, boolean>>({});
	let removing = $state<Entry | null>(null);

	const usenet = $derived((list?.entries ?? []).filter((e) => e.kind === 'usenet'));
	const debrid = $derived((list?.entries ?? []).filter((e) => e.kind === 'debrid'));

	async function load() {
		error = '';
		try {
			list = await api.get<Listing>('/connections');
		} catch (e) {
			error = e instanceof ApiError && e.status === 403 ? t('settings.providers.ownerOnly') : message(e);
		}
		loaded = true;
	}
	onMount(load);

	function start(kind: Kind) {
		form = blankForm(kind);
	}

	async function save(ev: SubmitEvent) {
		ev.preventDefault();
		if (!form) return;
		saving = true;
		try {
			const body = inputOf(form);
			const saved = form.id ? await api.put<Entry>(`/connections/${form.id}`, body) : await api.post<Entry>('/connections', body);
			if (saved.state === 'ok' || saved.state === 'disabled') toasts.push(t('settings.providers.saved', { name: saved.name }), 'ok');
			else toasts.push(t('settings.providers.savedNotPushed', { name: saved.name }), 'warn', { body: saved.lastError ?? '' });
			form = null;
			await load();
		} catch (e) {
			toasts.push(t('settings.providers.saveFailed'), 'fail', { body: message(e) });
		} finally {
			saving = false;
		}
	}

	async function run(e: Entry, what: 'test' | 'push') {
		busy[e.id + what] = true;
		try {
			if (what === 'test') {
				const r = await api.post<{ ok: boolean; text: string }>(`/connections/${e.id}/test`);
				toasts.push(r.ok ? t('settings.providers.testOk', { name: e.name }) : t('settings.providers.testFailed', { name: e.name }), r.ok ? 'ok' : 'fail', { body: r.text });
			} else {
				const v = await api.post<Entry>(`/connections/${e.id}/push`);
				toasts.push(v.state === 'ok' || v.state === 'disabled' ? t('settings.providers.pushed', { name: e.name }) : t('settings.providers.notPushed', { name: e.name }), v.state === 'ok' || v.state === 'disabled' ? 'ok' : 'fail', { body: v.lastError ?? '' });
				await load();
			}
		} catch (err) {
			toasts.push(t('settings.providers.actionFailed'), 'fail', { body: message(err) });
		} finally {
			busy[e.id + what] = false;
		}
	}

	async function remove(e: Entry, keepInTarget: boolean) {
		busy[e.id + 'rm'] = true;
		try {
			await api.del(`/connections/${e.id}${keepInTarget ? '?keepInTarget=1' : ''}`);
			toasts.push(t('settings.providers.removed', { name: e.name }), 'ok');
			removing = null;
			await load();
		} catch (err) {
			toasts.push(t('settings.providers.removeFailed'), 'fail', { body: message(err) });
		} finally {
			busy[e.id + 'rm'] = false;
		}
	}
</script>

<svelte:head><title>{t('settings.providers.title')} · HolyArrs</title></svelte:head>

<PageHeader title={t('settings.providers.title')} />

{#snippet rows(items: Entry[], kind: Kind)}
	{#each items as e (e.id)}
		<div class="srow" data-testid="provider-row">
			<div class="txt">
				<span class="label">{e.name}</span>
				<span class="hint">{kind === 'usenet' ? `${e.host}:${e.port}${e.ssl ? ' · SSL' : ''} · ${e.connections}` : e.provider === 'realdebrid' ? 'Real-Debrid' : 'AllDebrid'}{e.lastPush ? ` · ${t('settings.providers.pushedAgo', { when: ago(e.lastPush) })}` : ''}</span>
				{#if e.lastError}<span class="hint err" role="status">{e.lastError}</span>{/if}
			</div>
			<div class="ctl acts">
				<span class="st {e.state}"><i></i>{stateWord(e.state)}</span>
				<Button size="sm" onclick={() => run(e, 'test')} busy={busy[e.id + 'test']}>{t('settings.providers.test')}</Button>
				<Button size="sm" onclick={() => run(e, 'push')} busy={busy[e.id + 'push']}>{t('settings.providers.push')}</Button>
				<Button size="sm" onclick={() => (form = formOf(e))}>{t('settings.providers.edit')}</Button>
				<Button size="sm" variant="destructive" onclick={() => (removing = e)}>{t('settings.providers.remove')}</Button>
			</div>
		</div>
		{#if removing?.id === e.id}
			<div class="srow confirm" role="alert">
				<div class="txt"><span class="label">{t('settings.providers.removeAsk', { name: e.name })}</span><span class="hint">{kind === 'usenet' ? t('settings.providers.removeUsenetHint') : t('settings.providers.removeDebridHint')}</span></div>
				<div class="ctl acts">
					<Button size="sm" variant="destructive" onclick={() => remove(e, false)} busy={busy[e.id + 'rm']}>{t('settings.providers.removeBoth')}</Button>
					<Button size="sm" onclick={() => remove(e, true)}>{t('settings.providers.removeHereOnly')}</Button>
					<Button size="sm" variant="plain" onclick={() => (removing = null)}>{t('settings.providers.cancel')}</Button>
				</div>
			</div>
		{/if}
	{:else}
		<p class="empty">{kind === 'usenet' ? t('settings.providers.noUsenet') : t('settings.providers.noDebrid')}</p>
	{/each}
{/snippet}

{#if !loaded}
	<SkSettings rows={3} />
{:else if error}
	<LoadError hasData={false} message={error} onretry={load} />
{:else if list}
	{#if form}
		<SettingsCard title={form.id ? t('settings.providers.change', { name: form.name }) : form.kind === 'usenet' ? t('settings.providers.addUsenet') : t('settings.providers.addDebrid')}>
			<form class="pform" onsubmit={save} autocomplete="off">
				{#if form.kind === 'debrid' && !form.id}
					<Field id="pv-provider" label={t('settings.providers.provider')}>
						<select id="pv-provider" class="input" bind:value={form.provider} onchange={() => { if (form && /^holyarrs-(realdebrid|alldebrid)$/.test(form.name)) form.name = defaultDebridName(form.provider); }}>
							<option value="realdebrid">Real-Debrid</option>
							<option value="alldebrid">AllDebrid</option>
						</select>
					</Field>
				{/if}
				<Field id="pv-name" label={t('settings.providers.name')} hint={form.kind === 'debrid' ? t('settings.providers.nameDebridHint') : undefined}>
					<input id="pv-name" class="input" bind:value={form.name} maxlength="63" required />
				</Field>
				{#if form.kind === 'usenet'}
					<Field id="pv-host" label={t('settings.providers.host')}>
						<input id="pv-host" class="input" bind:value={form.host} maxlength="253" required placeholder="news.example.com" />
					</Field>
					<Field id="pv-port" label={t('settings.providers.port')}>
						<input id="pv-port" class="input" type="number" min="1" max="65535" bind:value={form.port} required />
					</Field>
					<label class="check"><input type="checkbox" bind:checked={form.ssl} /> {t('settings.providers.ssl')}</label>
					<Field id="pv-user" label={t('settings.providers.username')}>
						<input id="pv-user" class="input" bind:value={form.username} maxlength="200" />
					</Field>
				{/if}
				<Field id="pv-secret" label={form.kind === 'usenet' ? t('settings.providers.password') : t('settings.providers.apiKey')} hint={form.id ? t('settings.providers.secretKeep') : undefined}>
					<input id="pv-secret" class="input" type="password" bind:value={form.secret} maxlength="500" onfocus={() => { if (form && form.secret === MASKED) form.secret = ''; }} required={!form.id && form.kind === 'debrid'} />
				</Field>
				{#if form.kind === 'usenet'}
					<Field id="pv-conn" label={t('settings.providers.connections')}>
						<input id="pv-conn" class="input" type="number" min="1" max="100" bind:value={form.connections} required />
					</Field>
					<Field id="pv-prio" label={t('settings.providers.priority')} hint={t('settings.providers.priorityHint')}>
						<input id="pv-prio" class="input" type="number" min="0" max="99" bind:value={form.priority} />
					</Field>
				{/if}
				<label class="check"><input type="checkbox" bind:checked={form.enabled} /> {t('settings.providers.enabled')}</label>
				<div class="btns">
					<Button type="submit" variant="primary" busy={saving}>{t('settings.providers.save')}</Button>
					<Button variant="plain" onclick={() => (form = null)}>{t('settings.providers.cancel')}</Button>
				</div>
			</form>
		</SettingsCard>
	{/if}

	<SettingsCard id="usenet-card" title={t('settings.providers.usenetTitle')} description={list.sabnzbd.error ? list.sabnzbd.error : t('settings.providers.usenetAbout')}>
		{@render rows(usenet, 'usenet')}
		<div class="add"><Button size="sm" icon="plus" onclick={() => start('usenet')} disabled={!!form}>{t('settings.providers.addUsenet')}</Button></div>
	</SettingsCard>

	<SettingsCard id="debrid-card" title={t('settings.providers.debridTitle')} description={list.decypharr.error ? list.decypharr.error : t('settings.providers.debridAbout')}>
		{@render rows(debrid, 'debrid')}
		<div class="add"><Button size="sm" icon="plus" onclick={() => start('debrid')} disabled={!!form}>{t('settings.providers.addDebrid')}</Button></div>
	</SettingsCard>

	{#if list.unmanaged.length}
		<SettingsCard id="unmanaged-card" title={t('settings.providers.unmanagedTitle')} description={t('settings.providers.unmanagedAbout')}>
			{#each list.unmanaged as u (u.target + u.name)}
				<div class="srow"><div class="txt"><span class="label">{u.name}</span><span class="hint">{u.target === 'sabnzbd' ? 'SABnzbd' : 'Decypharr'}{u.detail ? ` · ${u.detail}` : ''}</span></div></div>
			{/each}
		</SettingsCard>
	{/if}
{/if}

<style>
	.acts { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; justify-content: flex-end; }
	.st { display: inline-flex; align-items: center; gap: 6px; color: var(--text-muted); font: 500 13px/1 var(--font-ui); }
	.st i { width: 7px; height: 7px; border-radius: 50%; background: var(--text-muted); }
	.st.ok i { background: var(--ok); }
	.st.pending i { background: var(--warn, orange); }
	.st.failed i { background: var(--fail); }
	.err { color: var(--fail-text); }
	.empty { margin: 0; padding: 13px 0; color: var(--text-muted); font: var(--t-subhead); font-size: 14px; }
	.add { padding: 12px 0 4px; }
	.pform { display: grid; gap: 14px; padding: 8px 0; }
	.check { display: flex; align-items: center; gap: 8px; font: var(--t-subhead); }
	.btns { display: flex; gap: 8px; }
	.confirm { background: color-mix(in srgb, var(--fail) 6%, transparent); }
</style>
