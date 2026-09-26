import type { Component } from 'vue'
import {
  AddIcon, ArrowLeftIcon, BookmarkIcon, BookmarkFilledIcon, BrowseIcon, BrowseOffIcon,
  ChartBarIcon, ChatIcon, CheckIcon, CheckCircleIcon, ChevronDownIcon, ChevronLeftIcon,
  ChevronUpIcon, CloseIcon, DeleteIcon, EarthIcon, EditIcon, ErrorCircleIcon, HeartIcon,
  HeartFilledIcon, HomeIcon, ImageIcon, ImageAddIcon, LinkIcon, LocationIcon, LockOffIcon,
  LockOnIcon, LogoutIcon, MailIcon, MoreIcon, NotificationIcon, RefreshIcon, SearchIcon,
  SettingIcon, ShareIcon, SmileIcon, SwapIcon, TimeIcon, UserIcon, UserAddIcon,
  UserCircleIcon, UserListIcon, ViewListIcon,
} from 'tdesign-icons-vue-next'

/** Icons bundled locally (no runtime download). Add names here as needed. */
export const ICONS: Record<string, Component> = {
  'add': AddIcon,
  'arrow-left': ArrowLeftIcon,
  'bookmark': BookmarkIcon,
  'bookmark-filled': BookmarkFilledIcon,
  'browse': BrowseIcon,
  'browse-off': BrowseOffIcon,
  'chart-bar': ChartBarIcon,
  'chat': ChatIcon,
  'check': CheckIcon,
  'check-circle': CheckCircleIcon,
  'chevron-down': ChevronDownIcon,
  'chevron-left': ChevronLeftIcon,
  'chevron-up': ChevronUpIcon,
  'close': CloseIcon,
  'delete': DeleteIcon,
  'earth': EarthIcon,
  'edit': EditIcon,
  'error-circle': ErrorCircleIcon,
  'heart': HeartIcon,
  'heart-filled': HeartFilledIcon,
  'home': HomeIcon,
  'image': ImageIcon,
  'image-add': ImageAddIcon,
  'link': LinkIcon,
  'location': LocationIcon,
  'lock-off': LockOffIcon,
  'lock-on': LockOnIcon,
  'logout': LogoutIcon,
  'mail': MailIcon,
  'more': MoreIcon,
  'notification': NotificationIcon,
  'refresh': RefreshIcon,
  'search': SearchIcon,
  'setting': SettingIcon,
  'share': ShareIcon,
  'smile': SmileIcon,
  'swap': SwapIcon,
  'time': TimeIcon,
  'user': UserIcon,
  'user-add': UserAddIcon,
  'user-circle': UserCircleIcon,
  'user-list': UserListIcon,
  'view-list': ViewListIcon,
}

export function resolveIcon(name: string): Component | undefined {
  return ICONS[name]
}

/** Numbers are pixel sizes; TDesign's named sizes and CSS lengths pass through. */
export function normalizeIconSize(size: number | string | undefined): string | undefined {
  if (size === undefined || size === null || size === '') return undefined
  if (typeof size === 'number') return `${size}px`
  return /^\d+(\.\d+)?$/.test(size) ? `${size}px` : size
}
