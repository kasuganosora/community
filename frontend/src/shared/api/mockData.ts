import type { MastodonAccount, MastodonStatus, MastodonTag, MastodonNotification } from '@/shared/types/mastodon'

// Use function to get current time for fresh data on each import
const getNow = () => new Date().toISOString()
const hour = (h: number) => new Date(Date.now() - h * 3600000).toISOString()

// Seeded random for deterministic test data
let seed = 12345
function random(): number {
  seed = (seed * 9301 + 49297) % 233280
  return seed / 233280
}

function randomInt(max: number): number {
  return Math.floor(random() * max)
}

export const mockCurrentUser: MastodonAccount = {
  id: '1',
  username: 'luna',
  acct: 'luna',
  display_name: 'Luna',
  locked: false,
  bot: false,
  discoverable: true,
  indexable: true,
  group: false,
  created_at: '2024-01-01T00:00:00Z',
  note: '<p>Community developer</p>',
  url: 'https://community.local/@luna',
  uri: 'https://community.local/users/luna',
  avatar: '',
  avatar_static: '',
  header: '',
  header_static: '',
  followers_count: 128,
  following_count: 64,
  statuses_count: 256,
  last_status_at: getNow(),
  hide_collections: false,
  emojis: [],
  fields: [],
}

const mockUsers: MastodonAccount[] = [
  {
    ...mockCurrentUser,
    id: '2',
    username: 'alice',
    acct: 'alice',
    display_name: 'Alice',
    note: '<p>Hello! I love coding.</p>',
    followers_count: 500,
    following_count: 200,
    statuses_count: 1024,
  },
  {
    ...mockCurrentUser,
    id: '3',
    username: 'bob',
    acct: 'bob',
    display_name: 'Bob',
    note: '<p>Open source enthusiast</p>',
    followers_count: 300,
    following_count: 150,
    statuses_count: 512,
  },
  {
    ...mockCurrentUser,
    id: '4',
    username: 'charlie',
    acct: 'charlie@remote.social',
    display_name: 'Charlie',
    note: '<p>Federated user from remote instance</p>',
    followers_count: 1200,
    following_count: 80,
    statuses_count: 2048,
  },
]

function makeStatus(id: string, account: MastodonAccount, content: string, hoursAgo: number, extra: Partial<MastodonStatus> = {}): MastodonStatus {
  return {
    id,
    created_at: hour(hoursAgo),
    in_reply_to_id: null,
    in_reply_to_account_id: null,
    sensitive: false,
    spoiler_text: '',
    visibility: 'public',
    language: 'en',
    uri: `https://community.local/statuses/${id}`,
    url: `https://community.local/@${account.username}/${id}`,
    replies_count: randomInt(20),
    reblogs_count: randomInt(50),
    favourites_count: randomInt(200),
    edited_at: null,
    favourited: false,
    reblogged: false,
    muted: false,
    bookmarked: false,
    pinned: false,
    content: `<p>${content}</p>`,
    reblog: null,
    application: null,
    account,
    media_attachments: [],
    mentions: [],
    tags: [],
    emojis: [],
    card: null,
    poll: null,
    ...extra,
  }
}

// Safe access with bounds checking
const alice = mockUsers[0] ?? mockCurrentUser
const bob = mockUsers[1] ?? mockCurrentUser
const charlie = mockUsers[2] ?? mockCurrentUser

export const mockStatuses: MastodonStatus[] = [
  makeStatus('101', alice, 'Just shipped a new feature! The federation support is looking great. 🚀', 0.5, {
    favourites_count: 42,
    reblogs_count: 12,
    replies_count: 5,
  }),
  makeStatus('102', bob, 'Working on some interesting Matrix protocol bridges today. The spec is quite detailed but fun to implement.', 1, {
    favourites_count: 28,
    reblogs_count: 8,
  }),
  makeStatus('103', charlie, 'Just discovered this amazing community platform. Feels like the early days of social media! Love the decentralized approach.', 2, {
    favourites_count: 156,
    reblogs_count: 34,
    replies_count: 12,
  }),
  makeStatus('104', mockCurrentUser, 'Thinking about implementing ActivityPub support next. The protocol is elegant in its simplicity.', 3, {
    favourites_count: 89,
    reblogs_count: 23,
    replies_count: 7,
    favourited: true,
  }),
  makeStatus('105', alice, 'Hot take: TypeScript + Vue 3 Composition API is the best DX for frontend development right now.', 5, {
    favourites_count: 234,
    reblogs_count: 67,
    replies_count: 45,
  }),
  makeStatus('106', bob, 'TIL: The Mastodon API is actually really well designed. REST with cursor-based pagination, clean auth flow. Respect.', 8, {
    favourites_count: 78,
    reblogs_count: 15,
  }),
  makeStatus('107', charlie, 'Good morning fediverse! What are you all working on today?', 12, {
    favourites_count: 45,
    reblogs_count: 3,
    replies_count: 22,
  }),
  makeStatus('108', mockCurrentUser, 'Just pushed the dark theme implementation. X.com vibes but decentralized 🌙', 14, {
    favourites_count: 167,
    reblogs_count: 41,
    replies_count: 18,
    reblogged: true,
  }),
]

export const mockTrends: MastodonTag[] = [
  { name: 'vue3', url: '/tags/vue3', history: [{ day: getNow(), uses: '128', accounts: '45' }] },
  { name: 'fediverse', url: '/tags/fediverse', history: [{ day: getNow(), uses: '89', accounts: '32' }] },
  { name: 'typescript', url: '/tags/typescript', history: [{ day: getNow(), uses: '256', accounts: '78' }] },
  { name: 'activitypub', url: '/tags/activitypub', history: [{ day: getNow(), uses: '67', accounts: '28' }] },
  { name: 'opensource', url: '/tags/opensource', history: [{ day: getNow(), uses: '345', accounts: '120' }] },
]

export const mockSuggestions: MastodonAccount[] = mockUsers.slice(0, 3)

export const mockNotifications: MastodonNotification[] = [
  {
    id: '1',
    type: 'favourite',
    created_at: hour(1),
    account: alice,
    status: mockStatuses[3],
  },
  {
    id: '2',
    type: 'reblog',
    created_at: hour(2),
    account: bob,
    status: mockStatuses[7],
  },
  {
    id: '3',
    type: 'follow',
    created_at: hour(3),
    account: charlie,
  },
  {
    id: '4',
    type: 'mention',
    created_at: hour(4),
    account: alice,
    status: makeStatus('201', alice, '@luna What do you think about the new federation features?', 4),
  },
]
