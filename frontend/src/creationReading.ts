import type { Message, PlaySession } from './types';

// Sequence identifies the same reading slot before and after scene acceptance.
export function creationReadingMessages(session: PlaySession, busy: boolean): Message[] {
  if (!busy || !session.run) return session.messages;
  const seq = session.messages.length + 1;
  return [...session.messages,
    { message_id: `${session.run.id}:input`, seq, kind: 'player', content: session.run.input, created_at: session.run.started_at },
    { message_id: `${session.run.id}:narrative`, seq: seq + 1, kind: 'narrative', content: session.run.candidate ?? '', created_at: session.run.started_at },
  ];
}
