<script setup lang="ts">
import SearchBox from '@/shared/components/widgets/SearchBox.vue'
import TrendingCard from '@/shared/components/widgets/TrendingCard.vue'
import WhoToFollowCard from '@/shared/components/widgets/WhoToFollowCard.vue'
</script>

<template>
  <div class="right-sidebar">
    <SearchBox />
    <TrendingCard />
    <WhoToFollowCard />
  </div>
</template>

<style scoped>
.right-sidebar {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: 8px;
}
</style>
