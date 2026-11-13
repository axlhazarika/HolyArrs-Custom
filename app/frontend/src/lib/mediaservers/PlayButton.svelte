<!-- ▶ Play: opens the movie or episode in the media server's web app, in a new tab (custom change). -->
<script lang="ts">
	import ActionButton from '#lib/ui/ActionButton.svelte';
	import { api } from '#lib/core/api.ts';
	import { t } from '#lib/core/i18n/i18n.svelte.ts';
	import { toasts } from '#lib/core/toasts.svelte.ts';
	import { plainMessage as message } from '#lib/core/settings.ts';
	import { openPlay, playPath, playShow, scanPath, type PlayAnswer } from './play.ts';

	let { titleId, itemId }: { titleId: number; itemId?: number } = $props();

	let answer = $state<PlayAnswer | null>(null);
	let scanning = $state(false);
	const show = $derived(playShow(answer));

	async function load() {
		try {
			answer = await api.get<PlayAnswer>(playPath(titleId, itemId));
		} catch {
			answer = null;
		}
	}

	$effect(() => {
		void titleId;
		void itemId;
		answer = null;
		void load();
	});

	async function scan() {
		scanning = true;
		try {
			await api.post(scanPath(titleId, itemId));
			toasts.push(t('play.scanAsked', { server: answer?.server ?? 'Jellyfin' }), 'ok');
			setTimeout(() => void load(), 30_000);
		} catch (e) {
			toasts.push(t('play.scanFailed'), 'fail', { body: message(e) });
		} finally {
			scanning = false;
		}
	}
</script>

{#if show === 'play' && answer}
	<ActionButton icon="play" pill label={t('play.label', { server: answer.server ?? 'Jellyfin' })} caption={t('play.caption')} onclick={() => answer && openPlay(answer.url)} data-testid="play" />
{:else if show === 'wait' && answer}
	<ActionButton icon="play" pill label={answer.state === 'not_added' ? t('play.notAdded', { server: answer.server ?? 'Jellyfin' }) : t('play.noAnswer', { server: answer.server ?? 'Jellyfin' })} caption={t('play.caption')} disabled data-testid="play" />
	{#if answer.can_scan && answer.state === 'not_added'}
		<ActionButton icon="refresh" label={t('play.scan', { server: answer.server ?? 'Jellyfin' })} busy={scanning} onclick={scan} data-testid="play-scan" />
	{/if}
{/if}
