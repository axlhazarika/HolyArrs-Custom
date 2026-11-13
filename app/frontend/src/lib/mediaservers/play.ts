// The Play link of a movie or episode (custom change): the server answers
// a link into the media server's web app, or why there is none yet.

export type PlayState = 'ready' | 'no_file' | 'no_server' | 'not_added' | 'no_answer';

export interface PlayAnswer {
	url: string;
	state: PlayState;
	server?: string;
	why?: string;
	can_scan?: boolean;
}

/** playPath is the route for a Title's movie, or one of its episodes. */
export function playPath(titleId: number, itemId?: number): string {
	return `/titles/${titleId}/play` + (itemId ? `?item=${itemId}` : '');
}

/** scanPath asks the media server to look for the file now. */
export function scanPath(titleId: number, itemId?: number): string {
	return `/titles/${titleId}/play/scan` + (itemId ? `?item=${itemId}` : '');
}

/**
 * How the button shows: a working link; a disabled button that says why
 * (the server has not added the file, or did not answer); or nothing, when
 * there is no file or no media server to play from.
 */
export function playShow(a: PlayAnswer | null): 'play' | 'wait' | 'none' {
	if (!a) return 'none';
	if (a.state === 'ready' && a.url) return 'play';
	if (a.state === 'not_added' || a.state === 'no_answer') return 'wait';
	return 'none';
}

/** openPlay opens the link in a new tab that cannot reach back into HolyArrs. */
export function openPlay(url: string, open: typeof window.open = window.open.bind(window)): void {
	if (!/^https?:\/\//i.test(url)) return;
	open(url, '_blank', 'noopener,noreferrer');
}
