<!-- The address viewers' browsers use for a media server's web app (custom change): Play links go there. Owner only. -->
<script lang="ts">
	import Field from '#lib/ui/Field.svelte';
	import Button from '#lib/ui/Button.svelte';
	import { api } from '#lib/core/api.ts';
	import { t } from '#lib/core/i18n/i18n.svelte.ts';
	import { toasts } from '#lib/core/toasts.svelte.ts';
	import { plainMessage as message } from '#lib/core/settings.ts';

	let { serverId }: { serverId: number } = $props();

	let value = $state('');
	let saved = $state('');
	let busy = $state(false);

	$effect(() => {
		const id = serverId;
		void api
			.get<{ play_url: string }>(`/media-servers/${id}/play-url`)
			.then((r) => {
				value = saved = r.play_url;
			})
			.catch(() => {});
	});

	async function save() {
		busy = true;
		try {
			const r = await api.put<{ play_url: string }>(`/media-servers/${serverId}/play-url`, { play_url: value });
			value = saved = r.play_url;
			toasts.push(t('play.viewerAddressSaved'), 'ok');
		} catch (e) {
			toasts.push(t('play.viewerAddressFailed'), 'fail', { body: message(e) });
		} finally {
			busy = false;
		}
	}
</script>

<Field id="ms-play-url" label={t('play.viewerAddress')}>
	<div class="pu">
		<input id="ms-play-url" class="input mono" bind:value autocomplete="off" spellcheck="false" placeholder="http://192.168.1.20:8096" aria-describedby="ms-play-url-hint" />
		<Button type="button" disabled={busy || value.trim() === saved} aria-busy={busy} onclick={save}>{t('play.viewerAddressSave')}</Button>
	</div>
	<span class="hint" id="ms-play-url-hint">{t('play.viewerAddressHint')}</span>
</Field>

<style>
	.pu { display: flex; gap: 8px; align-items: center; }
	.pu input { flex: 1; min-width: 0; }
</style>
