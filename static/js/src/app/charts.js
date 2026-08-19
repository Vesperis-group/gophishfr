var GophishCharts = (function () {
    var centerTextPlugin = {
        id: 'centerText',
        afterDraw: function (chart, args, options) {
            if (!options || options.text === undefined || !chart.chartArea) {
                return
            }
            var chartArea = chart.chartArea
            var context = chart.ctx
            context.save()
            context.fillStyle = options.color
            context.font = 'bold ' + options.fontSize + 'px Helvetica, Arial, sans-serif'
            context.textAlign = 'center'
            context.textBaseline = 'middle'
            context.fillText(
                String(options.text),
                (chartArea.left + chartArea.right) / 2,
                (chartArea.top + chartArea.bottom) / 2
            )
            context.restore()
        }
    }

    Chart.register(centerTextPlugin)

    function getCanvas(elementId) {
        var canvas = document.getElementById(elementId)
        if (!(canvas instanceof HTMLCanvasElement)) {
            throw new Error('Chart canvas not found: ' + elementId)
        }
        return canvas
    }

    function getChart(elementId) {
        var chart = Chart.getChart(elementId)
        if (!chart) {
            throw new Error('Chart not initialized: ' + elementId)
        }
        return chart
    }

    function formatDate(timestamp) {
        return moment(timestamp).format('dddd, MMM DD h:mm:ss a')
    }

    function formatAxisDate(timestamp, range) {
        if (range <= 60 * 1000) {
            return moment(timestamp).format('h:mm:ss a')
        }
        if (range <= 24 * 60 * 60 * 1000) {
            return moment(timestamp).format('h:mm a')
        }
        if (range <= 31 * 24 * 60 * 60 * 1000) {
            return moment(timestamp).format('MMM DD, h:mm a')
        }
        if (range <= 365 * 24 * 60 * 60 * 1000) {
            return moment(timestamp).format('MMM DD, YYYY')
        }
        return moment(timestamp).format('MMM YYYY')
    }

    function doughnutLabel(title, data) {
        var count = data[0].count
        var percentage = data[0].y
        return title + ': ' + count + ' recipients (' + percentage + '%)'
    }

    function configureDoughnutAccessibility(chart, title, data) {
        chart.canvas.setAttribute('role', 'img')
        chart.canvas.setAttribute('aria-label', doughnutLabel(title, data))
    }

    function showZoomResetButton(chart) {
        var button = chart.canvas.parentNode.querySelector('.chart-reset-zoom')
        if (button) {
            button.style.display = 'block'
        }
    }

    function addZoomResetButton(chart) {
        var button = document.createElement('button')
        button.type = 'button'
        button.className = 'btn btn-secondary btn-sm chart-reset-zoom'
        button.textContent = 'Reset zoom'
        button.style.display = 'none'
        button.addEventListener('click', function () {
            chart.resetZoom()
            button.style.display = 'none'
        })
        chart.canvas.parentNode.appendChild(button)
    }

    function horizontalZoomOptions() {
        return {
            limits: {
                x: {
                    min: 'original',
                    max: 'original'
                }
            },
            zoom: {
                drag: {
                    enabled: true,
                    backgroundColor: 'rgba(66, 139, 202, 0.15)',
                    borderColor: '#428bca',
                    borderWidth: 1
                },
                pinch: {
                    enabled: true
                },
                mode: 'x',
                onZoomComplete: function (context) {
                    showZoomResetButton(context.chart)
                }
            }
        }
    }

    function renderDoughnut(chartOptions) {
        var chart = new Chart(getCanvas(chartOptions.elemId), {
            type: 'doughnut',
            data: {
                labels: chartOptions.data.map(function (point) {
                    return point.name
                }),
                datasets: [{
                    data: chartOptions.data.map(function (point) {
                        return point.y
                    }),
                    backgroundColor: chartOptions.colors,
                    borderWidth: 0
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                cutout: '80%',
                plugins: {
                    centerText: {
                        text: chartOptions.data[0].count,
                        color: chartOptions.colors[0],
                        fontSize: chartOptions.centerFontSize
                    },
                    legend: {
                        display: false
                    },
                    title: {
                        display: true,
                        text: chartOptions.title,
                        color: '#333333',
                        font: {
                            size: 18
                        }
                    },
                    tooltip: {
                        callbacks: {
                            label: function (context) {
                                if (!context.label) {
                                    return null
                                }
                                return context.label + ': ' + context.parsed + '%'
                            }
                        }
                    }
                }
            }
        })
        configureDoughnutAccessibility(chart, chartOptions.title, chartOptions.data)
        return chart
    }

    function updateDoughnut(elementId, data) {
        var chart = getChart(elementId)
        chart.data.labels = data.map(function (point) {
            return point.name
        })
        chart.data.datasets[0].data = data.map(function (point) {
            return point.y
        })
        chart.options.plugins.centerText.text = data[0].count
        configureDoughnutAccessibility(chart, chart.options.plugins.title.text, data)
        chart.update()
    }

    function renderTimeline(chartOptions) {
        var chart = new Chart(getCanvas(chartOptions.elemId), {
            type: 'line',
            data: {
                datasets: [{
                    data: chartOptions.data,
                    borderColor: '#cccccc',
                    borderDash: [4, 4],
                    borderWidth: 1,
                    pointBackgroundColor: chartOptions.data.map(function (point) {
                        return point.color
                    }),
                    pointBorderColor: chartOptions.data.map(function (point) {
                        return point.color
                    }),
                    pointRadius: 3,
                    pointHoverRadius: 4,
                    tension: 0
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                parsing: false,
                plugins: {
                    legend: {
                        display: false
                    },
                    title: {
                        display: true,
                        text: chartOptions.title,
                        color: '#333333',
                        font: {
                            size: 18
                        }
                    },
                    tooltip: {
                        callbacks: {
                            title: function (items) {
                                return items.length ? formatDate(items[0].raw.x) : ''
                            },
                            label: function (context) {
                                return [
                                    'Event: ' + context.raw.message,
                                    'Email: ' + context.raw.email
                                ]
                            }
                        }
                    },
                    zoom: horizontalZoomOptions()
                },
                scales: {
                    x: {
                        type: 'linear',
                        ticks: {
                            callback: function (value) {
                                return formatAxisDate(value, this.max - this.min)
                            }
                        }
                    },
                    y: {
                        display: false,
                        min: 0,
                        max: 2
                    }
                }
            }
        })
        addZoomResetButton(chart)
        chart.canvas.setAttribute('role', 'img')
        chart.canvas.setAttribute(
            'aria-label',
            chartOptions.title + ': ' + chartOptions.data.length + ' events'
        )
        return chart
    }

    function updateTimeline(elementId, data) {
        var chart = getChart(elementId)
        chart.data.datasets[0].data = data
        chart.data.datasets[0].pointBackgroundColor = data.map(function (point) {
            return point.color
        })
        chart.data.datasets[0].pointBorderColor = data.map(function (point) {
            return point.color
        })
        chart.canvas.setAttribute(
            'aria-label',
            chart.options.plugins.title.text + ': ' + data.length + ' events'
        )
        chart.update()
    }

    function renderOverview(chartOptions) {
        var chart = new Chart(getCanvas(chartOptions.elemId), {
            type: 'line',
            data: {
                datasets: [{
                    data: chartOptions.data,
                    backgroundColor: 'rgba(240, 91, 79, 0.5)',
                    borderColor: '#f05b4f',
                    borderWidth: 2,
                    fill: true,
                    pointBackgroundColor: '#f05b4f',
                    pointBorderColor: '#f05b4f',
                    pointRadius: 3,
                    pointHoverRadius: 4,
                    tension: 0.4
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                parsing: false,
                onClick: function (event, elements, clickedChart) {
                    if (!elements.length) {
                        return
                    }
                    var element = elements[0]
                    var point = clickedChart.data.datasets[element.datasetIndex].data[element.index]
                    window.location.href = '/campaigns/' + point.campaign_id
                },
                onHover: function (event, elements, hoveredChart) {
                    hoveredChart.canvas.style.cursor = elements.length ? 'pointer' : 'default'
                },
                plugins: {
                    legend: {
                        display: false
                    },
                    title: {
                        display: true,
                        text: chartOptions.title,
                        color: '#333333',
                        font: {
                            size: 18
                        }
                    },
                    tooltip: {
                        callbacks: {
                            title: function (items) {
                                return items.length ? formatDate(items[0].raw.x) : ''
                            },
                            label: function (context) {
                                return [
                                    context.raw.name,
                                    '% Success: ' + context.raw.y + '%'
                                ]
                            }
                        }
                    },
                    zoom: horizontalZoomOptions()
                },
                scales: {
                    x: {
                        type: 'linear',
                        ticks: {
                            callback: function (value) {
                                return formatAxisDate(value, this.max - this.min)
                            }
                        }
                    },
                    y: {
                        min: 0,
                        max: 100,
                        title: {
                            display: true,
                            text: '% of Success'
                        }
                    }
                }
            }
        })
        addZoomResetButton(chart)
        chart.canvas.setAttribute('role', 'img')
        chart.canvas.setAttribute(
            'aria-label',
            chartOptions.title + ': ' + chartOptions.data.length + ' campaigns'
        )
        return chart
    }

    return {
        renderDoughnut: renderDoughnut,
        renderOverview: renderOverview,
        renderTimeline: renderTimeline,
        updateDoughnut: updateDoughnut,
        updateTimeline: updateTimeline
    }
})()
