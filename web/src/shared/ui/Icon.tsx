interface IconProps {
  name: 'play' | 'room' | 'bot' | 'rank' | 'stats' | 'counter' | 'history' | 'rules' | 'close' | 'sound' | 'muted' | 'copy' | 'check' | 'share';
}

export function Icon({ name }: IconProps) {
  const path = icons[name];
  return (
    <svg className="icon" viewBox="0 0 24 24" aria-hidden="true">
      <path d={path} />
    </svg>
  );
}

const icons: Record<IconProps['name'], string> = {
  sound: 'M3 9h4l5-4v14l-5-4H3V9Zm12-2a7 7 0 0 1 0 10v-3a4 4 0 0 0 0-4V7Zm3-3a11 11 0 0 1 0 16v-3a8 8 0 0 0 0-10V4Z',
  muted: 'M3 9h4l5-4v14l-5-4H3V9Zm12-1 3 3 3-3 1 1-3 3 3 3-1 1-3-3-3 3-1-1 3-3-3-3 1-1Z',
  play: 'M8 5v14l11-7-11-7Z',
  room: 'M12 3 2 11h3v9h5v-6h4v6h5v-9h3L12 3Z',
  bot: 'M7 8h10a3 3 0 0 1 3 3v5a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3v-5a3 3 0 0 1 3-3Zm2-4h6v2H9V4Zm0 8h2v2H9v-2Zm4 0h2v2h-2v-2Z',
  rank: 'M5 20V9h4v11H5Zm5 0V4h4v16h-4Zm5 0v-7h4v7h-4Z',
  stats: 'M5 4h14v4H5V4Zm0 6h9v4H5v-4Zm0 6h12v4H5v-4Z',
  counter: 'M4 4h16v16H4V4Zm3 3v3h3V7H7Zm0 5v5h3v-5H7Zm5-5v3h5V7h-5Zm0 5v5h5v-5h-5Z',
  history: 'M12 5a7 7 0 1 1-6.3 4H3l3.5-4L10 9H7.9A5 5 0 1 0 12 7v5l4 2-.9 1.8-5.1-2.6V5h2Z',
  rules: 'M5 4h14v16H5V4Zm3 4h8V6H8v2Zm0 4h8v-2H8v2Zm0 4h5v-2H8v2Z',
  close: 'm6 7.4 1.4-1.4 4.6 4.6L16.6 6 18 7.4 13.4 12l4.6 4.6-1.4 1.4-4.6-4.6L7.4 18 6 16.6l4.6-4.6L6 7.4Z',
  copy: 'M8 4h10a2 2 0 0 1 2 2v10h-2V6H8V4Zm-2 4h10a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V10a2 2 0 0 1 2-2Zm0 2v10h10V10H6Z',
  check: 'm9.5 16.6-4.6-4.6 1.4-1.4 3.2 3.2 8.2-8.2 1.4 1.4-9.6 9.6Z',
  share: 'M18 16a3 3 0 0 0-2.4 1.2L8.9 13.7a3.2 3.2 0 0 0 0-3.4l6.7-3.5A3 3 0 1 0 15 5a3 3 0 0 0 .1.7L8.4 9.2a3 3 0 1 0 0 5.6l6.7 3.5a3 3 0 1 0 2.9-2.3Z'
};
