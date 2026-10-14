<script setup lang="ts">
import AdvertisementCard from '@/shared/components/widgets/AdvertisementCard.vue'
import SearchBox from '@/shared/components/widgets/SearchBox.vue'
import TrendingCard from '@/shared/components/widgets/TrendingCard.vue'
import WhoToFollowCard from '@/shared/components/widgets/WhoToFollowCard.vue'
import { useRoute } from 'vue-router'
const route = useRoute()
</script>

<template>
  <div class="right-sidebar">
    <SearchBox v-if="route.name !== 'search'" />
    <AdvertisementCard />
    <TrendingCard />
    <WhoToFollowCard />
  </div>
</template>

<style scoped>
.right-sidebar {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding-bottom: var(--space-8);
}
</style>
