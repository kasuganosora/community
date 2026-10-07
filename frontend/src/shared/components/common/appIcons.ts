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
// Filled / state variants used by the navigation chrome and appearance settings.
import {
  ChevronRightIcon, DesktopIcon, HomeFilledIcon, MailFilledIcon, ModeDarkIcon, ModeLightIcon,
  NotificationFilledIcon, SettingFilledIcon, UserCircleFilledIcon,
} from 'tdesign-icons-vue-next'

import { QuoteIcon, DashboardIcon, UsergroupIcon, FlagIcon, HappyIcon, InternetIcon, ServerIcon, DataIcon, MenuIcon, ArrowRightUpIcon, ArrowRightIcon } from 'tdesign-icons-vue-next'

/** Icons bundled locally (no runtime download). Add names here as needed. */
export const ICONS: Record<string, Component> = {
  'quote': QuoteIcon,
  'dashboard': DashboardIcon,
  'usergroup': UsergroupIcon,
  'flag': FlagIcon,
  'happy': HappyIcon,
  'internet': InternetIcon,
  'server': ServerIcon,
  'data': DataIcon,
  'menu': MenuIcon,
  'arrow-top-right': ArrowRightUpIcon,
  'arrow-right': ArrowRightIcon,

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
  'chevron-right': ChevronRightIcon,
  'chevron-up': ChevronUpIcon,
  'close': CloseIcon,
  'delete': DeleteIcon,
  'desktop': DesktopIcon,
  'earth': EarthIcon,
  'edit': EditIcon,
  'error-circle': ErrorCircleIcon,
  'heart': HeartIcon,
  'heart-filled': HeartFilledIcon,
  'home': HomeIcon,
  'home-filled': HomeFilledIcon,
  'image': ImageIcon,
  'image-add': ImageAddIcon,
  'link': LinkIcon,
  'location': LocationIcon,
  'lock-off': LockOffIcon,
  'lock-on': LockOnIcon,
  'logout': LogoutIcon,
  'mail': MailIcon,
  'mail-filled': MailFilledIcon,
  'mode-dark': ModeDarkIcon,
  'mode-light': ModeLightIcon,
  'more': MoreIcon,
  'notification': NotificationIcon,
  'notification-filled': NotificationFilledIcon,
  'refresh': RefreshIcon,
  'search': SearchIcon,
  'setting': SettingIcon,
  'setting-filled': SettingFilledIcon,
  'share': ShareIcon,
  'smile': SmileIcon,
  'swap': SwapIcon,
  'time': TimeIcon,
  'user': UserIcon,
  'user-add': UserAddIcon,
  'user-circle': UserCircleIcon,
  'user-circle-filled': UserCircleFilledIcon,
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
